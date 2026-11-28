package ipc

// DefaultListen creates a per-session acceptor. Platform files set the
// implementation in init (Windows named pipe; unsupported elsewhere).
var DefaultListen ListenFunc
