package main

import (
	"bufio"
	"encoding/json"
	"sync"
)

// conn is one live websocket, addressable by the player on the other end so a message can be pushed
// to them from whichever connection caused it. Writes are serialised: a push runs on the sender's
// goroutine, not the receiver's read loop.
type conn struct {
	mu sync.Mutex
	rw *bufio.ReadWriter
}

func (c *conn) sendJSON(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	return writeText(c.rw, payload)
}

type connections struct {
	mu sync.RWMutex
	m  map[string]*conn
}

func (cs *connections) add(id string, c *conn) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.m == nil {
		cs.m = map[string]*conn{}
	}
	cs.m[id] = c
}

// remove only drops the entry if it is still the one registered, so a player who reconnected while
// an old read loop was finishing keeps their new connection.
func (cs *connections) remove(id string, c *conn) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.m[id] == c {
		delete(cs.m, id)
	}
}

func (cs *connections) get(id string) *conn {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	return cs.m[id]
}

// sendRaw writes an already-encoded frame, taking the same lock as sendJSON.
func (c *conn) sendRaw(payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return writeText(c.rw, payload)
}
