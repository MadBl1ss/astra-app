// Package ipc is the channel between the daemon and its clients: a named
// pipe on Windows, a unix socket elsewhere, JSON-RPC 2.0 with one object per
// line, and topic subscriptions for the daemon's notifications.
package ipc

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"strelka/proto"
)

const (
	// DefaultName is the channel of the installed service, DevName the one
	// `strelkad run` uses so a dev build never collides with it.
	DefaultName = "strelka"
	DevName     = "strelka-dev"

	// maxRequestLine bounds what one client line may cost the daemon.
	// Requests are tiny; the largest planned one is a pasted subscription.
	maxRequestLine = 1 << 20

	// sendQueue is how many messages may wait for one client. State is sent
	// at most ten times a second, so a client that falls this far behind has
	// stopped reading; it is dropped rather than allowed to hold memory.
	sendQueue = 64
)

// Handler answers one request. It returns either a result (nil means {}) or
// an error with a user-facing text. It is called from the connection's read
// loop, one request at a time per connection.
type Handler func(c *Conn, method string, params json.RawMessage) (any, *proto.Error)

// Server accepts connections and fans notifications out to subscribers.
type Server struct {
	handler Handler

	mu     sync.Mutex
	conns  map[*Conn]struct{}
	closed bool
	wg     sync.WaitGroup
}

func NewServer(h Handler) *Server {
	return &Server{handler: h, conns: map[*Conn]struct{}{}}
}

// Serve accepts until the listener is closed. It returns nil on a clean stop.
func (s *Server) Serve(l net.Listener) error {
	for {
		nc, err := l.Accept()
		if err != nil {
			if s.isClosed() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		c := &Conn{
			srv:    s,
			nc:     nc,
			out:    make(chan []byte, sendQueue),
			done:   make(chan struct{}),
			topics: map[string]bool{},
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			nc.Close()
			return nil
		}
		s.conns[c] = struct{}{}
		s.wg.Add(2)
		s.mu.Unlock()
		go c.readLoop()
		go c.writeLoop()
	}
}

// Close drops every client and waits for their goroutines. The listener is
// the caller's to close.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	conns := make([]*Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	s.wg.Wait()
}

// Drain waits, up to limit, until every client's queue has been handed to
// the OS. Called before Close so the last notifications are not cut off.
func (s *Server) Drain(limit time.Duration) {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		pending := 0
		s.mu.Lock()
		for c := range s.conns {
			pending += len(c.out) + int(c.writing.Load())
		}
		s.mu.Unlock()
		if pending == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *Server) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Clients is the number of live connections.
func (s *Server) Clients() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Publish sends a notification to every client subscribed to topic. The
// method name of the notification is the topic itself. It never blocks: a
// client whose queue is full is disconnected.
func (s *Server) Publish(topic string, params any) {
	s.mu.Lock()
	var subs []*Conn
	for c := range s.conns {
		if c.subscribed(topic) {
			subs = append(subs, c)
		}
	}
	s.mu.Unlock()
	if len(subs) == 0 {
		return
	}
	line, err := notification(topic, params)
	if err != nil {
		return
	}
	for _, c := range subs {
		c.send(line)
	}
}

func (s *Server) remove(c *Conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
}

// Conn is one client connection.
type Conn struct {
	srv  *Server
	nc   net.Conn
	out  chan []byte
	done chan struct{}
	once sync.Once

	mu     sync.Mutex
	topics map[string]bool

	writing atomic.Int32 // 1 while a message is between the queue and the OS
}

// Subscribe adds topics to what this client receives.
func (c *Conn) Subscribe(topics ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range topics {
		c.topics[t] = true
	}
}

func (c *Conn) Unsubscribe(topics ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range topics {
		delete(c.topics, t)
	}
}

func (c *Conn) subscribed(topic string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.topics[topic]
}

// Notify sends one notification to this client only.
func (c *Conn) Notify(method string, params any) {
	if line, err := notification(method, params); err == nil {
		c.send(line)
	}
}

// Close drops the client. Safe to call more than once and from any goroutine.
func (c *Conn) Close() {
	c.once.Do(func() {
		close(c.done)
		c.nc.Close() // unblocks a pending Read and a pending Write
		c.srv.remove(c)
	})
}

func (c *Conn) send(line []byte) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.out <- line:
	default:
		// The queue is full: the client is not reading. Cutting it off is
		// cheaper than buffering without bound, and it will reconnect and
		// ask for the whole state anyway.
		c.Close()
	}
}

func (c *Conn) writeLoop() {
	defer c.srv.wg.Done()
	for {
		select {
		case <-c.done:
			return
		case line := <-c.out:
			c.writing.Store(1)
			_, err := c.nc.Write(line)
			c.writing.Store(0)
			if err != nil {
				c.Close()
				return
			}
		}
	}
}

func (c *Conn) readLoop() {
	defer c.srv.wg.Done()
	defer c.Close()
	sc := bufio.NewScanner(c.nc)
	sc.Buffer(make([]byte, 0, 4096), maxRequestLine)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req proto.Message
		if err := json.Unmarshal(line, &req); err != nil {
			c.reply(nil, nil, proto.Errorf(proto.CodeParseError, "Запрос не разобран: это не JSON."))
			continue
		}
		if req.Method == "" {
			// Not a request. Clients have nothing to answer to, so a stray
			// response is ignored rather than treated as an error.
			if len(req.ID) > 0 && req.Result == nil && req.Error == nil {
				c.reply(req.ID, nil, proto.Errorf(proto.CodeInvalidRequest, "В запросе нет метода."))
			}
			continue
		}
		result, rerr := c.srv.handler(c, req.Method, req.Params)
		if len(req.ID) == 0 {
			continue // a notification gets no answer, even on error
		}
		c.reply(req.ID, result, rerr)
	}
}

func (c *Conn) reply(id json.RawMessage, result any, rerr *proto.Error) {
	msg := proto.Message{JSONRPC: "2.0", ID: id}
	if len(id) == 0 {
		msg.ID = json.RawMessage("null")
	}
	if rerr != nil {
		msg.Error = rerr
	} else {
		raw := json.RawMessage("{}")
		if result != nil {
			b, err := json.Marshal(result)
			if err != nil {
				msg.Error = proto.Errorf(proto.CodeInvalid, "Служба не смогла собрать ответ.")
			} else {
				raw = b
			}
		}
		if msg.Error == nil {
			msg.Result = raw
		}
	}
	line, err := json.Marshal(msg)
	if err != nil {
		return
	}
	c.send(append(line, '\n'))
}

func notification(method string, params any) ([]byte, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	line, err := json.Marshal(proto.Message{JSONRPC: "2.0", Method: method, Params: raw})
	if err != nil {
		return nil, err
	}
	return append(line, '\n'), nil
}
