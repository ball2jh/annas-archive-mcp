package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// errBrowserClosed is returned for calls made after Chrome has gone away.
var errBrowserClosed = errors.New("the browser closed before the check finished")

// cdpConn speaks the Chrome DevTools Protocol over Chrome's debugging pipe:
// JSON messages, each terminated by a NUL byte.
type cdpConn struct {
	w    io.WriteCloser
	stop func() // asks Chrome to exit and reaps the process

	mu      sync.Mutex
	nextID  int
	pending map[int]chan cdpReply
	closed  bool
}

type cdpReply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// newCDPConn starts reading replies from r. stop is run once by close.
func newCDPConn(r io.Reader, w io.WriteCloser, stop func()) *cdpConn {
	c := &cdpConn{w: w, stop: stop, pending: make(map[int]chan cdpReply)}
	go c.readLoop(r)
	return c
}

func (c *cdpConn) readLoop(r io.Reader) {
	reader := bufio.NewReader(r)
	for {
		frame, err := reader.ReadBytes(0)
		if err != nil {
			break
		}
		var msg struct {
			ID int `json:"id"`
			cdpReply
		}
		if json.Unmarshal(frame[:len(frame)-1], &msg) != nil || msg.ID == 0 {
			continue // an event, which nothing here listens for
		}
		c.mu.Lock()
		ch := c.pending[msg.ID]
		delete(c.pending, msg.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- msg.cdpReply
		}
	}

	// The pipe is gone: fail every call still waiting, and all later ones.
	c.mu.Lock()
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

// call sends one command and decodes its result into out (which may be nil).
// sessionID addresses a tab; empty addresses the browser itself.
func (c *cdpConn) call(ctx context.Context, sessionID, method string, params, out any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errBrowserClosed
	}
	c.nextID++
	id := c.nextID
	ch := make(chan cdpReply, 1)
	c.pending[id] = ch

	frame, err := json.Marshal(struct {
		ID        int    `json:"id"`
		Method    string `json:"method"`
		Params    any    `json:"params,omitempty"`
		SessionID string `json:"sessionId,omitempty"`
	}{id, method, params, sessionID})
	if err == nil {
		_, err = c.w.Write(append(frame, 0))
	}
	if err != nil {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case reply, ok := <-ch:
		if !ok {
			return errBrowserClosed
		}
		if reply.Error != nil {
			return fmt.Errorf("%s: %s", method, reply.Error.Message)
		}
		if out == nil {
			return nil
		}
		return json.Unmarshal(reply.Result, out)
	}
}

// close shuts the browser down and releases the pipe.
func (c *cdpConn) close() {
	c.stop()
	_ = c.w.Close()
}
