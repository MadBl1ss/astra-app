package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"strelka/proto"
)

var pipeSeq atomic.Int64

// testName gives every test its own channel so parallel packages and a
// running dev daemon never meet.
func testName() string {
	return fmt.Sprintf("strelka-test-ipc-%d-%d", os.Getpid(), pipeSeq.Add(1))
}

// answerNow makes a handler of a function that answers in place. "slow"
// sleeps in the wait, as the daemon's waiting for its loop does, and the
// rest answer at once; subscriptions happen in the read loop, in order.
func answerNow(f func(c *Conn, method string, params json.RawMessage) (any, *proto.Error)) Handler {
	return func(c *Conn, method string, params json.RawMessage) func() (any, *proto.Error) {
		if method == "slow" {
			return func() (any, *proto.Error) { return f(c, method, params) }
		}
		result, err := f(c, method, params)
		return func() (any, *proto.Error) { return result, err }
	}
}

// startServer runs a server with a small test protocol: echo, subscribe, fail.
func startServer(t *testing.T) (*Server, string) {
	t.Helper()
	name := testName()
	l, err := Listen(name)
	if err != nil {
		t.Fatalf("канал не открылся: %v", err)
	}
	srv := NewServer(answerNow(func(c *Conn, method string, params json.RawMessage) (any, *proto.Error) {
		switch method {
		case "echo":
			return params, nil
		case "empty":
			return nil, nil
		case "slow":
			time.Sleep(300 * time.Millisecond)
			return "slow", nil
		case "subscribe":
			var p proto.TopicsParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, proto.Errorf(proto.CodeInvalidParams, "Неверные параметры.")
			}
			c.Subscribe(p.Topics...)
			return nil, nil
		case "unsubscribe":
			var p proto.TopicsParams
			json.Unmarshal(params, &p)
			c.Unsubscribe(p.Topics...)
			return nil, nil
		}
		return nil, proto.Errorf(proto.CodeMethodNotFound, "Неизвестный метод %s.", method)
	}))
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	t.Cleanup(func() {
		l.Close()
		srv.Close()
		if err := <-served; err != nil {
			t.Errorf("Serve завершился с ошибкой: %v", err)
		}
	})
	return srv, name
}

func dialTest(t *testing.T, name string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Dial(ctx, name)
	if err != nil {
		t.Fatalf("не подключился к каналу: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func ctx5(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestRequestAndResponse(t *testing.T) {
	_, name := startServer(t)
	c := dialTest(t, name)

	var got map[string]string
	if err := c.Call(ctx5(t), "echo", map[string]string{"привет": "служба"}, &got); err != nil {
		t.Fatal(err)
	}
	if got["привет"] != "служба" {
		t.Errorf("ответ %v", got)
	}

	var empty map[string]any
	if err := c.Call(ctx5(t), "empty", nil, &empty); err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("пустой результат должен быть {}: %v, %v", empty, err)
	}

	err := c.Call(ctx5(t), "nope", nil, nil)
	var perr *proto.Error
	if !errors.As(err, &perr) || perr.Code != proto.CodeMethodNotFound {
		t.Fatalf("жду ошибку -32601, получил %v", err)
	}
	if !strings.Contains(perr.Message, "nope") {
		t.Errorf("текст ошибки: %q", perr.Message)
	}
}

// A slow answer does not hold up a quick request sent after it on the same
// connection: the window has one connection, and a connection check that
// takes half a minute must not freeze its switch.
func TestSlowRequestDoesNotHoldUpTheNext(t *testing.T) {
	_, name := startServer(t)
	c := dialTest(t, name)
	slow := make(chan error, 1)
	go func() {
		var s string
		slow <- c.Call(ctx5(t), "slow", nil, &s)
	}()
	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	var got string
	if err := c.Call(ctx5(t), "echo", "быстро", &got); err != nil || got != "быстро" {
		t.Fatalf("echo: %q, %v", got, err)
	}
	if took := time.Since(start); took > 200*time.Millisecond {
		t.Errorf("быстрый запрос ждал медленный: %v", took)
	}
	if err := <-slow; err != nil {
		t.Errorf("медленный: %v", err)
	}
}

func TestBrokenLineDoesNotDropTheConnection(t *testing.T) {
	_, name := startServer(t)
	nc, err := dial(ctx5(t), name)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(nc)
	defer c.Close()
	if _, err := nc.Write([]byte("это не json\n")); err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := c.Call(ctx5(t), "echo", map[string]int{"a": 1}, &got); err != nil || got["a"] != 1 {
		t.Errorf("после битой строки соединение должно жить: %v, %v", got, err)
	}
}

func TestSubscriptionDeliversOnlyChosenTopics(t *testing.T) {
	srv, name := startServer(t)
	c := dialTest(t, name)
	if err := c.Call(ctx5(t), "subscribe", proto.TopicsParams{Topics: []string{"state"}}, nil); err != nil {
		t.Fatal(err)
	}

	srv.Publish("journal", map[string]int{"n": 1}) // not subscribed
	srv.Publish("state", map[string]int{"rev": 1})
	srv.Publish("state", map[string]int{"rev": 2})

	for want := 1; want <= 2; want++ {
		select {
		case n := <-c.Notifications():
			var p map[string]int
			json.Unmarshal(n.Params, &p)
			if n.Method != "state" || p["rev"] != want {
				t.Fatalf("уведомление %s %s, жду state rev=%d", n.Method, n.Params, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("уведомление не пришло")
		}
	}

	if err := c.Call(ctx5(t), "unsubscribe", proto.TopicsParams{Topics: []string{"state"}}, nil); err != nil {
		t.Fatal(err)
	}
	srv.Publish("state", map[string]int{"rev": 3})
	// A call after the publish proves the server had its chance to send it.
	if err := c.Call(ctx5(t), "empty", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case n := <-c.Notifications():
		t.Errorf("после отписки пришло %s %s", n.Method, n.Params)
	default:
	}
}

func TestSlowClientIsDisconnected(t *testing.T) {
	srv, name := startServer(t)

	// The slow client subscribes and then never reads again.
	slow, err := dial(ctx5(t), name)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	if _, err := slow.Write([]byte(`{"jsonrpc":"2.0","method":"subscribe","params":{"topics":["state"]}}` + "\n")); err != nil {
		t.Fatal(err)
	}

	healthy := dialTest(t, name)
	if err := healthy.Call(ctx5(t), "subscribe", proto.TopicsParams{Topics: []string{"state"}}, nil); err != nil {
		t.Fatal(err)
	}
	received := make(chan int, 1)
	go func() {
		n := 0
		for range healthy.Notifications() {
			n++
		}
		received <- n
	}()
	waitFor(t, "оба клиента подписаны", func() bool { return srv.Clients() == 2 })

	// 30 KB is the size of a State with a hundred servers.
	payload := map[string]string{"pad": strings.Repeat("x", 30*1024)}
	sent := 0
	deadline := time.Now().Add(10 * time.Second)
	for srv.Clients() == 2 && time.Now().Before(deadline) {
		srv.Publish("state", payload)
		sent++
		// Paced so the healthy client, which does read, keeps up.
		time.Sleep(time.Millisecond)
	}
	if srv.Clients() != 1 {
		t.Fatalf("медленный клиент не отключён после %d сообщений", sent)
	}

	// The healthy one is still served.
	if err := healthy.Call(ctx5(t), "empty", nil, nil); err != nil {
		t.Fatalf("здоровый клиент пострадал: %v", err)
	}
	healthy.Close()
	if n := <-received; n == 0 {
		t.Error("здоровый клиент не получил ни одного уведомления")
	}
}

func TestTwoClientsAtOnce(t *testing.T) {
	_, name := startServer(t)
	const calls = 200
	var wg sync.WaitGroup
	errs := make(chan error, 2*calls)
	for client := 0; client < 2; client++ {
		c := dialTest(t, name)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < calls; i++ {
				want := fmt.Sprintf("клиент %d, запрос %d", client, i)
				var got map[string]string
				if err := c.Call(context.Background(), "echo", map[string]string{"v": want}, &got); err != nil {
					errs <- err
					return
				}
				if got["v"] != want {
					errs <- fmt.Errorf("чужой ответ: %q вместо %q", got["v"], want)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestSecondListenerOnTheSameNameFails(t *testing.T) {
	_, name := startServer(t)
	l, err := Listen(name)
	if err == nil {
		l.Close()
		t.Fatal("второй слушатель на том же канале должен получить ошибку")
	}
}

func TestCallFailsWhenServerGoesAway(t *testing.T) {
	name := testName()
	l, err := Listen(name)
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	srv := NewServer(func(*Conn, string, json.RawMessage) func() (any, *proto.Error) {
		return func() (any, *proto.Error) {
			<-block
			return nil, nil
		}
	})
	go srv.Serve(l)
	c := dialTest(t, name)

	result := make(chan error, 1)
	go func() { result <- c.Call(context.Background(), "hang", nil, nil) }()
	waitFor(t, "клиент подключился", func() bool { return srv.Clients() == 1 })
	l.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(block)
	}()
	srv.Close()

	select {
	case err := <-result:
		// Either the daemon's answer raced the close or the call saw the
		// connection end; what must not happen is a call that hangs.
		if err != nil && !errors.Is(err, ErrClosed) {
			t.Errorf("неожиданная ошибка: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("вызов завис после остановки службы")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("не дождался: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
