package web

import (
	"sync"
)

// Hub manages SSE client subscriptions and broadcasts events.
// thread-safe for concurrent subscribe/unsubscribe/broadcast operations.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
}

// NewHub creates a new SSE hub.
func NewHub() *Hub {
	return &Hub{
		clients: make(map[chan Event]struct{}),
	}
}

// Subscribe adds a client channel to receive events.
// returns a buffered channel that will receive broadcast events.
// the returned channel has buffer size of 256 to handle burst events.
func (h *Hub) Subscribe() chan Event {
	ch := make(chan Event, 256)

	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()

	return ch
}

// Unsubscribe removes a client channel and closes it.
// safe to call multiple times with the same channel.
func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[ch]; ok {
		delete(h.clients, ch)
		close(ch)
	}
}

// Broadcast sends an event to all subscribed clients.
// uses non-blocking send to prevent slow clients from blocking.
// events are dropped for clients with full buffers.
func (h *Hub) Broadcast(e Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for ch := range h.clients {
		select {
		case ch <- e:
			// sent successfully
		default:
			// client buffer full, drop event
			// this prevents slow clients from blocking the broadcast
		}
	}
}

// ClientCount returns the number of currently connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.clients)
}

// Close unsubscribes all clients and closes their channels.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for ch := range h.clients {
		close(ch)
		delete(h.clients, ch)
	}
}
