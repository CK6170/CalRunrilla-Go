package server

import (
	"encoding/json"
	"sync"

	"github.com/gorilla/websocket"
)

type WSMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data,omitempty"`
}

type WSClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (c *WSClient) Send(msg WSMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteJSON(msg)
}

type WSHub struct {
	mu      sync.RWMutex
	clients map[*WSClient]struct{}
}

func NewWSHub() *WSHub {
	return &WSHub{clients: make(map[*WSClient]struct{})}
}

func (h *WSHub) Add(conn *websocket.Conn) *WSClient {
	c := &WSClient{conn: conn}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *WSHub) Remove(c *WSClient) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	_ = c.conn.Close()
}

func (h *WSHub) Broadcast(msg WSMessage) {
	// Marshal once for consistency across clients
	b, _ := json.Marshal(msg)
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		c.mu.Lock()
		_ = c.conn.WriteMessage(websocket.TextMessage, b)
		c.mu.Unlock()
	}
}

