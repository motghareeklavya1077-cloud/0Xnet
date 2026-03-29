package websocket

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// signalingMessage is used to extract routing fields from incoming WS messages
type signalingMessage struct {
	Type         string `json:"type"`
	TargetPeerID string `json:"targetPeerId"`
}

// ServeWS handles WebSocket connections for WebRTC signaling.
// Expects query params: ?session=<sessionID>&peerId=<peerID>
func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	peerID := r.URL.Query().Get("peerId")

	if sessionID == "" || peerID == "" {
		http.Error(w, "session and peerId query params required", http.StatusBadRequest)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ [WS] Upgrade failed: %v", err)
		return
	}

	// Register this peer in the session hub
	client := hub.Join(sessionID, peerID, conn)

	// Ensure cleanup on disconnect
	defer hub.Leave(sessionID, peerID)

	// Read loop — relay signaling messages
	for {
		_, rawMsg, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("⚠️ [WS] Unexpected close from peer %s: %v", client.PeerID, err)
			}
			break
		}

		// Parse just the routing fields
		var sig signalingMessage
		if err := json.Unmarshal(rawMsg, &sig); err != nil {
			log.Printf("⚠️ [WS] Invalid JSON from peer %s: %v", peerID, err)
			continue
		}

		switch sig.Type {
		case "rtc-offer", "rtc-answer", "ice-candidate":
			// Relay to the specific target peer
			if sig.TargetPeerID == "" {
				log.Printf("⚠️ [WS] %s from peer %s missing targetPeerId", sig.Type, peerID)
				continue
			}
			hub.RelayTo(sessionID, sig.TargetPeerID, json.RawMessage(rawMsg))

		default:
			// Broadcast any other message type to all peers in the session
			hub.Broadcast(sessionID, peerID, json.RawMessage(rawMsg))
		}
	}
}
