package web

import (
	"errors"
	"log"
	"sync"
	"sync/atomic"
)

// MaxClients is the maximum number of concurrent SSE connections allowed.
// prevents DoS attacks via connection exhaustion.
const MaxClients = 100

// ErrMaxClientsExceeded is returned when the connection limit is reached.
var ErrMaxClientsExceeded = errors.New("max clients exceeded")

// Hub manages SSE client subscriptions and broadcasts events.
// thread-safe for concurrent subscribe/unsubscribe/broadcast operations.
type Hub struct {
	mu            sync.RWMutex
	clients       map[chan Event]struct{}
	droppedEvents atomic.Int64 // counter for dropped events due to slow clients
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
// returns ErrMaxClientsExceeded if the connection limit is reached.
func (h *Hub) Subscribe() (chan Event, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.clients) >= MaxClients {
		return nil, ErrMaxClientsExceeded
	}

	ch := make(chan Event, 256)
	h.clients[ch] = struct{}{}

	return ch, nil
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
// events are dropped for clients with full buffers (tracked and logged periodically).
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
			dropped := h.droppedEvents.Add(1)
			// log every 100 dropped events to avoid log spam
			if dropped%100 == 0 {
				log.Printf("[WARN] SSE hub dropped %d events total (slow clients)", dropped)
			}
		}
	}
}

// ClientCount returns the number of currently connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.clients)
}

// DroppedEvents returns the total number of events dropped due to slow clients.
func (h *Hub) DroppedEvents() int64 {
	return h.droppedEvents.Load()
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
