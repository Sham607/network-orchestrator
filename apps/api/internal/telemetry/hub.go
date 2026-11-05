package telemetry

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Hub manages active WebSocket subscriber connections and broadcasts telemetry.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[*subscriber]struct{}
	logger      *slog.Logger
}

type subscriber struct {
	conn   *websocket.Conn
	msgs   chan []byte
	closed chan struct{}
}

// NewHub constructs a thread-safe telemetry Hub.
func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		subscribers: make(map[*subscriber]struct{}),
		logger:      logger,
	}
}

// Handler handles incoming HTTP upgrade requests to WebSocket.
func (h *Hub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			h.logger.Warn("telemetry: websocket accept failed", "error", err)
			return
		}

		sub := &subscriber{
			conn:   conn,
			msgs:   make(chan []byte, 32),
			closed: make(chan struct{}),
		}

		h.addSubscriber(sub)
		defer h.removeSubscriber(sub)

		// Goroutine for writing buffered messages out to WebSocket
		go func() {
			for {
				select {
				case <-sub.closed:
					return
				case msg, ok := <-sub.msgs:
					if !ok {
						return
					}
					writeCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
					err := sub.conn.Write(writeCtx, websocket.MessageText, msg)
					cancel()
					if err != nil {
						return
					}
				}
			}
		}()

		// Read loop to detect disconnects / ping-pong
		for {
			_, _, err := sub.conn.Read(r.Context())
			if err != nil {
				break
			}
		}
	}
}

func (h *Hub) addSubscriber(s *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribers[s] = struct{}{}
}

func (h *Hub) removeSubscriber(s *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subscribers[s]; ok {
		delete(h.subscribers, s)
		close(s.closed)
		_ = s.conn.Close(websocket.StatusNormalClosure, "disconnect")
	}
}

// Broadcast sends a Message to all connected subscribers concurrently.
func (h *Hub) Broadcast(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		h.logger.Error("telemetry: marshal message failed", "error", err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for sub := range h.subscribers {
		select {
		case sub.msgs <- data:
		default:
			// Buffer full, drop frame to prevent lagging
		}
	}
}
