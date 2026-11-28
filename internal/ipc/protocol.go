package ipc

import (
	"encoding/json"
	"fmt"
	"time"
)

// ProtocolVersion is the exact session-ipc protocol version (documented in
// docs/ARCHITECTURE.md). Bump only with a coordinated agent+service change.
const ProtocolVersion = 1

// MaxFrameBytes is the hard upper bound on a single framed payload (64 KiB).
const MaxFrameBytes = 64 * 1024

// HelloTimeout is how long the server waits for the first hello after accept.
const HelloTimeout = 5 * time.Second

// Message type names (JSON "type" field).
const (
	TypeHello    = "hello"
	TypeHelloAck = "hello_ack"
	TypeReject   = "reject"
	TypePing     = "ping"
	TypePong     = "pong"
	TypeMsg      = "msg"
)

// Reject codes.
const (
	RejectVersionMismatch = "version_mismatch"
	RejectBadHello        = "bad_hello"
	RejectDuplicateHello  = "duplicate_hello"
	RejectProtocol        = "protocol_error"
)

// Message is the JSON payload inside a length-prefixed frame.
type Message struct {
	Type            string          `json:"type"`
	Protocol        int             `json:"protocol,omitempty"`
	AgentVersion    string          `json:"agent_version,omitempty"`
	SessionID       uint32          `json:"session_id,omitempty"`
	PID             uint32          `json:"pid,omitempty"`
	IntervalSeconds int             `json:"interval_seconds,omitempty"`
	ServerTime      string          `json:"server_time,omitempty"`
	Code            string          `json:"code,omitempty"`
	Message         string          `json:"message,omitempty"`
	MsgType         string          `json:"msg_type,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

// DecodeMessage parses JSON into Message. Unknown fields are ignored.
func DecodeMessage(data []byte) (Message, error) {
	if len(data) == 0 {
		return Message{}, fmt.Errorf("ipc: empty message")
	}
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return Message{}, fmt.Errorf("ipc: json: %w", err)
	}
	if m.Type == "" {
		return Message{}, fmt.Errorf("ipc: missing type")
	}
	return m, nil
}

// EncodeMessage marshals m to JSON (compact).
func EncodeMessage(m Message) ([]byte, error) {
	if m.Type == "" {
		return nil, fmt.Errorf("ipc: missing type")
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("ipc: marshal: %w", err)
	}
	return b, nil
}

// NewHello builds a client hello.
func NewHello(protocol int, agentVersion string, sessionID, pid uint32) Message {
	return Message{
		Type:         TypeHello,
		Protocol:     protocol,
		AgentVersion: agentVersion,
		SessionID:    sessionID,
		PID:          pid,
	}
}

// NewHelloAck builds a server hello_ack.
func NewHelloAck(protocol, intervalSeconds int, serverTime time.Time) Message {
	return Message{
		Type:            TypeHelloAck,
		Protocol:        protocol,
		IntervalSeconds: intervalSeconds,
		ServerTime:      serverTime.UTC().Format(time.RFC3339Nano),
	}
}

// NewReject builds a reject message.
func NewReject(code, message string) Message {
	return Message{Type: TypeReject, Code: code, Message: message}
}

// NewPing / NewPong are keepalive messages.
func NewPing() Message { return Message{Type: TypePing} }
func NewPong() Message { return Message{Type: TypePong} }

// NewMsg builds the T-23+ extension envelope.
func NewMsg(msgType string, payload json.RawMessage) Message {
	return Message{Type: TypeMsg, MsgType: msgType, Payload: payload}
}
