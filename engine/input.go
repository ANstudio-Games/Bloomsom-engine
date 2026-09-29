package engine

import (
	"encoding/json"
	"time"
)

// Input encapsulates an authoritative player input command received over the network.
type Input struct {
	// Seq is the client-provided input sequence number (used for client-side reconciliation).
	Seq uint32
	// Type is the input action type.
	Type string
	// Payload contains raw action parameters (JSON or binary).
	Payload json.RawMessage
	// ReceivedAt records the timestamp when the server received the input.
	ReceivedAt time.Time
}
