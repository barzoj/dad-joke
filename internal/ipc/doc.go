// Package ipc implements the versioned named-pipe protocol between the
// Windows Service (LocalSystem, Session 0) and each session agent
// (`dad-joke.exe session`). Contract version: session-ipc/v1.
//
// # ProtocolVersion
//
// ProtocolVersion is 1. Exact match only — no negotiation. A client that
// advertises any other value receives reject{code:"version_mismatch"} and the
// connection is closed; the session agent is not marked connected.
//
// # Framing
//
// Each message is a length-prefixed frame: 4-byte big-endian unsigned length
// followed by a UTF-8 JSON payload. MaxFrameBytes is 64 KiB (65536). Frames
// larger than the limit, truncated headers/payloads, and non-JSON payloads
// close the connection (fail closed). The decoder never allocates more than
// MaxFrameBytes for a single frame.
//
// # Messages
//
//	hello       {type, protocol, agent_version, session_id, pid}
//	hello_ack   {type, protocol, interval_seconds, server_time}
//	reject      {type, code, message}
//	ping / pong {type}
//	msg         {type:"msg", msg_type, payload}  // extension envelope (T-23+)
//
// Unknown type values are ignored and counted; they never crash the peer.
//
// # Transport
//
// Windows: named pipe `\\.\pipe\DadJoke.session.<sessionID>` with
// FILE_FLAG_FIRST_PIPE_INSTANCE and PIPE_REJECT_REMOTE_CLIENTS. DACL allows
// SYSTEM and the interactive session's user SID only. After ConnectNamedPipe
// the service verifies GetNamedPipeClientProcessId and
// GetNamedPipeClientSessionId against the child it launched.
//
// Non-Windows: in-memory net.Pipe fakes so protocol tests run on Linux.
//
// # Trust
//
// Everything received from the session agent is untrusted input. The IPC
// carries no secrets (no token, no backend URL). The service sends only the
// sampling interval in hello_ack.
package ipc
