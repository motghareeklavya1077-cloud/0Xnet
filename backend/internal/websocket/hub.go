package websocket

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
)

// Client represents a single WebSocket peer in a session
type Client struct {
	PeerID    string
	SessionID string
	Conn      *websocket.Conn
	mu        sync.Mutex
}

// SendJSON sends a JSON message to the client (thread-safe)
func (c *Client) SendJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Conn.WriteJSON(v)
}

// Hub manages all active WebSocket connections grouped by session
type Hub struct {
	// sessions maps sessionID → (peerID → *Client)
	sessions map[string]map[string]*Client
	mu       sync.RWMutex
}

// NewHub creates a new Hub instance
func NewHub() *Hub {
	return &Hub{
		sessions: make(map[string]map[string]*Client),
	}
}

// Join registers a client in a session and notifies existing peers
func (h *Hub) Join(sessionID, peerID string, conn *websocket.Conn) *Client {
	client := &Client{
		PeerID:    peerID,
		SessionID: sessionID,
		Conn:      conn,
	}

	h.mu.Lock()
	if h.sessions[sessionID] == nil {
		h.sessions[sessionID] = make(map[string]*Client)
	}

	// Collect existing peer IDs before adding the new one
	existingPeers := make([]string, 0, len(h.sessions[sessionID]))
	for pid := range h.sessions[sessionID] {
		existingPeers = append(existingPeers, pid)
	}

	h.sessions[sessionID][peerID] = client
	h.mu.Unlock()

	log.Printf("🔌 [Hub] Peer %s joined session %s (%d peers now)", peerID, sessionID, len(existingPeers)+1)

	// Notify existing peers that a new peer joined
	joinMsg := map[string]string{
		"type":   "peer-joined",
		"peerId": peerID,
	}
	for _, pid := range existingPeers {
		h.sendToPeer(sessionID, pid, joinMsg)
	}

	// Tell the new peer about all existing peers so it can create connections
	if len(existingPeers) > 0 {
		peersMsg := map[string]interface{}{
			"type":  "existing-peers",
			"peers": existingPeers,
		}
		client.SendJSON(peersMsg)
	}

	return client
}

// Leave removes a client from a session and notifies remaining peers
func (h *Hub) Leave(sessionID, peerID string) {
	h.mu.Lock()
	if peers, ok := h.sessions[sessionID]; ok {
		if client, exists := peers[peerID]; exists {
			client.Conn.Close()
			delete(peers, peerID)
		}
		// Clean up empty sessions
		if len(peers) == 0 {
			delete(h.sessions, sessionID)
		}
	}
	h.mu.Unlock()

	log.Printf("🔌 [Hub] Peer %s left session %s", peerID, sessionID)

	// Notify remaining peers
	leaveMsg := map[string]string{
		"type":   "peer-left",
		"peerId": peerID,
	}
	h.Broadcast(sessionID, peerID, leaveMsg)
}

// RelayTo forwards a message to a specific peer in a session
func (h *Hub) RelayTo(sessionID, targetPeerID string, msg json.RawMessage) {
	h.mu.RLock()
	peers, ok := h.sessions[sessionID]
	if !ok {
		h.mu.RUnlock()
		return
	}
	target, exists := peers[targetPeerID]
	h.mu.RUnlock()

	if exists {
		target.SendJSON(json.RawMessage(msg))
	}
}

// Broadcast sends a message to all peers in a session except the sender
func (h *Hub) Broadcast(sessionID, senderPeerID string, msg interface{}) {
	h.mu.RLock()
	peers, ok := h.sessions[sessionID]
	if !ok {
		h.mu.RUnlock()
		return
	}
	// Copy pointers to avoid holding lock during sends
	targets := make([]*Client, 0, len(peers))
	for pid, client := range peers {
		if pid != senderPeerID {
			targets = append(targets, client)
		}
	}
	h.mu.RUnlock()

	for _, client := range targets {
		client.SendJSON(msg)
	}
}

// sendToPeer sends a message to a specific peer (internal helper)
func (h *Hub) sendToPeer(sessionID, peerID string, msg interface{}) {
	h.mu.RLock()
	peers, ok := h.sessions[sessionID]
	if !ok {
		h.mu.RUnlock()
		return
	}
	client, exists := peers[peerID]
	h.mu.RUnlock()

	if exists {
		client.SendJSON(msg)
	}
}
