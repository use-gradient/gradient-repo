package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/gradient/gradient/pkg/livectx"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins; tighten in production via config
	},
	HandshakeTimeout: 10 * time.Second,
}

// handleWebSocketEvents provides a WebSocket endpoint for real-time event streaming.
// Clients connect via ws:///api/v1/events/ws?branch=<branch>
// Events are pushed as JSON frames as they arrive. Clients can also send JSON
// messages to publish events (same schema as POST /api/v1/events).
func (s *Server) handleWebSocketEvents(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		writeError(w, http.StatusBadRequest, "branch query parameter is required")
		return
	}
	envIDFilter := r.URL.Query().Get("env_id")
	typeFilter := r.URL.Query().Get("type")

	// Disable server WriteTimeout before upgrading — WebSocket connections are long-lived
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Time{})

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("[ws] client connected: org=%s branch=%s", orgID, branch)

	// Set up ping/pong for keepalive
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// We need a way to send events to the WebSocket. Since the Bus.Subscribe
	// doesn't return a subscription ID we can cancel, we use a context.
	subCtx, subCancel := context.WithCancel(r.Context())
	defer subCancel()

	// Mutex to protect concurrent writes to the WebSocket
	var writeMu sync.Mutex

	// Subscribe to events via the event bus
	consumerSuffix := "ws-" + conn.RemoteAddr().String()
	err = s.eventBus.Subscribe(subCtx, orgID, branch, consumerSuffix, func(_ context.Context, event *livectx.Event) error {
		// Apply filters
		if envIDFilter != "" && event.EnvID != envIDFilter {
			return nil
		}
		if typeFilter != "" && string(event.Type) != typeFilter {
			return nil
		}

		data, err := json.Marshal(event)
		if err != nil {
			return nil
		}

		writeMu.Lock()
		defer writeMu.Unlock()

		conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("[ws] write error: %v", err)
			subCancel()
			return err
		}
		return nil
	})
	if err != nil {
		log.Printf("[ws] subscribe failed: %v", err)
		conn.WriteJSON(map[string]string{"error": "failed to subscribe to events"})
		return
	}

	// Send initial connection message
	writeMu.Lock()
	conn.WriteJSON(map[string]interface{}{
		"type":    "connected",
		"branch":  branch,
		"org_id":  orgID,
		"message": "WebSocket connected. Events will stream in real-time.",
	})
	writeMu.Unlock()

	// Ping ticker for keepalive
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	done := make(chan struct{})

	// Read loop: handle incoming messages (publish events) and detect disconnects
	go func() {
		defer close(done)
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					log.Printf("[ws] unexpected close: %v", err)
				}
				return
			}

			// Try to parse incoming message as an event to publish
			var event livectx.Event
			if err := json.Unmarshal(message, &event); err != nil {
				writeMu.Lock()
				conn.WriteJSON(map[string]string{"error": "invalid event format: " + err.Error()})
				writeMu.Unlock()
				continue
			}

			// Set org/branch context
			event.OrgID = orgID
			if event.Branch == "" {
				event.Branch = branch
			}

			if err := event.Validate(); err != nil {
				writeMu.Lock()
				conn.WriteJSON(map[string]string{"error": "validation failed: " + err.Error()})
				writeMu.Unlock()
				continue
			}

			if err := s.meshPublisher.Publish(r.Context(), &event); err != nil {
				writeMu.Lock()
				conn.WriteJSON(map[string]string{"error": "publish failed: " + err.Error()})
				writeMu.Unlock()
				continue
			}

			writeMu.Lock()
			conn.WriteJSON(map[string]interface{}{
				"type":     "published",
				"event_id": event.ID,
			})
			writeMu.Unlock()
		}
	}()

	// Main loop: wait for done or send pings
	for {
		select {
		case <-done:
			log.Printf("[ws] client disconnected: org=%s branch=%s", orgID, branch)
			return
		case <-subCtx.Done():
			return
		case <-pingTicker.C:
			writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				writeMu.Unlock()
				log.Printf("[ws] ping failed: %v", err)
				return
			}
			writeMu.Unlock()
		}
	}
}
