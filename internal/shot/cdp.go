package shot

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// cdp speaks the Chrome DevTools Protocol over --remote-debugging-pipe:
// JSON messages separated by NUL bytes, no WebSocket needed.
type cdp struct {
	w       io.Writer
	writeMu sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[int64]chan message
	subs    map[*subscription]bool
	closed  chan struct{}
	err     error
}

type message struct {
	ID        int64           `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type subscription struct {
	session string
	methods map[string]bool
	ch      chan message
}

func newCDP(w io.Writer, r io.Reader) *cdp {
	c := &cdp{w: w, pending: map[int64]chan message{}, subs: map[*subscription]bool{}, closed: make(chan struct{})}
	go c.read(r)
	return c
}

func (c *cdp) read(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	var err error
	for {
		var raw []byte
		raw, err = br.ReadBytes(0)
		if err != nil {
			break
		}
		var m message
		if json.Unmarshal(raw[:len(raw)-1], &m) != nil {
			continue
		}
		c.mu.Lock()
		if m.ID != 0 {
			if ch := c.pending[m.ID]; ch != nil {
				ch <- m
				delete(c.pending, m.ID)
			}
		} else {
			for s := range c.subs {
				if s.session == m.SessionID && s.methods[m.Method] {
					select {
					case s.ch <- m:
					default: // a waiter that stopped listening must not block the reader
					}
				}
			}
		}
		c.mu.Unlock()
	}
	c.mu.Lock()
	c.err = fmt.Errorf("browser connection closed: %w", err)
	close(c.closed)
	c.mu.Unlock()
}

// call sends one command and decodes its result into out (if not nil).
func (c *cdp) call(ctx context.Context, session, method string, params, out any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.next++
	id := c.next
	ch := make(chan message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	data, err := json.Marshal(struct {
		ID        int64  `json:"id"`
		Method    string `json:"method"`
		Params    any    `json:"params,omitempty"`
		SessionID string `json:"sessionId,omitempty"`
	}{id, method, params, session})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	_, err = c.w.Write(append(data, 0))
	c.writeMu.Unlock()
	if err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		if out != nil && len(m.Result) > 0 {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-c.closed:
		return c.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

// subscribe collects events of one session. Subscribe before triggering the
// action whose events matter.
func (c *cdp) subscribe(session string, methods ...string) *subscription {
	s := &subscription{session: session, methods: map[string]bool{}, ch: make(chan message, 1024)}
	for _, m := range methods {
		s.methods[m] = true
	}
	c.mu.Lock()
	c.subs[s] = true
	c.mu.Unlock()
	return s
}

func (c *cdp) unsubscribe(s *subscription) {
	c.mu.Lock()
	delete(c.subs, s)
	c.mu.Unlock()
}

// wait returns the first event on s for which match reports true.
func (c *cdp) wait(ctx context.Context, s *subscription, match func(message) bool) (message, error) {
	for {
		select {
		case m := <-s.ch:
			if match(m) {
				return m, nil
			}
		case <-c.closed:
			return message{}, c.err
		case <-ctx.Done():
			return message{}, ctx.Err()
		}
	}
}
