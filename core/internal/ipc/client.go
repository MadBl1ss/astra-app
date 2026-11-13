package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"sync"

	"strelka/proto"
)

// maxReplyLine bounds one line from the daemon. A State with a hundred
// servers is about 30 KB; this leaves room for a very long server list.
const maxReplyLine = 16 << 20

// ErrClosed is returned by calls on a connection that has ended.
var ErrClosed = errors.New("соединение со службой закрыто")

// Notification is a message the daemon sent on its own.
type Notification struct {
	Method string
	Params json.RawMessage
}

// Client is the caller's side of the channel, used by strelkactl and tests.
type Client struct {
	nc   net.Conn
	done chan struct{}

	// notes carries notifications in order. The reader blocks when it is
	// full, so a subscriber must keep draining it or the daemon will drop
	// the connection as a slow client.
	notes chan Notification

	wmu sync.Mutex // serializes writes

	mu      sync.Mutex
	pending map[uint64]chan proto.Message
	nextID  uint64
	once    sync.Once
}

// Dial connects to the daemon's channel by its short name.
func Dial(ctx context.Context, name string) (*Client, error) {
	nc, err := dial(ctx, name)
	if err != nil {
		return nil, err
	}
	return NewClient(nc), nil
}

// NewClient wraps an established connection.
func NewClient(nc net.Conn) *Client {
	c := &Client{
		nc:      nc,
		done:    make(chan struct{}),
		notes:   make(chan Notification, 256),
		pending: map[uint64]chan proto.Message{},
	}
	go c.readLoop()
	return c
}

// Notifications is closed when the connection ends.
func (c *Client) Notifications() <-chan Notification { return c.notes }

// Done is closed when the connection ends, whoever ended it.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Close() error {
	c.once.Do(func() {
		close(c.done)
		c.nc.Close()
	})
	return nil
}

// Call sends a request and waits for its answer. An error from the daemon
// comes back as *proto.Error with a text ready for the user.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	msg := proto.Message{JSONRPC: "2.0", Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return err
		}
		msg.Params = raw
	}

	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan proto.Message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	msg.ID = json.RawMessage(strconv.FormatUint(id, 10))
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	_, err = c.nc.Write(append(line, '\n'))
	c.wmu.Unlock()
	if err != nil {
		c.Close()
		return ErrClosed
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result == nil || len(resp.Result) == 0 {
			return nil
		}
		return json.Unmarshal(resp.Result, result)
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
}

func (c *Client) readLoop() {
	defer close(c.notes)
	defer c.Close()
	sc := bufio.NewScanner(c.nc)
	sc.Buffer(make([]byte, 0, 64*1024), maxReplyLine)
	for sc.Scan() {
		var msg proto.Message
		if err := json.Unmarshal(sc.Bytes(), &msg); err != nil {
			continue
		}
		if msg.IsNotification() {
			select {
			case c.notes <- Notification{Method: msg.Method, Params: msg.Params}:
			case <-c.done:
				return
			}
			continue
		}
		id, err := strconv.ParseUint(string(msg.ID), 10, 64)
		if err != nil {
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		c.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}
