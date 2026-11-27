package jobs

import (
	"io"
	"sync"
)

// cappedWriter drains all writes (never blocks the pipe) while keeping at most
// max bytes of captured output, truncated later on a UTF-8 boundary.
type cappedWriter struct {
	mu        sync.Mutex
	buf       []byte
	max       int
	truncated bool
}

func newCappedWriter(max int) *cappedWriter {
	if max < 0 {
		max = 0
	}
	return &cappedWriter{max: max, buf: make([]byte, 0, min(max, 4096))}
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) < w.max {
		need := w.max - len(w.buf)
		if len(p) <= need {
			w.buf = append(w.buf, p...)
		} else {
			w.buf = append(w.buf, p[:need]...)
			w.truncated = true
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	// Always report full accept so the pipe drains (no OOM from unbounded grow,
	// no writer stall from a filled buffer).
	return len(p), nil
}

func (w *cappedWriter) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := TruncateUTF8(append([]byte(nil), w.buf...), w.max)
	return out
}

func (w *cappedWriter) String() string {
	return string(w.Bytes())
}

func (w *cappedWriter) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated || len(w.buf) > w.max
}

// discardWriter is used only when max is 0; still drains.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

var _ io.Writer = (*cappedWriter)(nil)
var _ io.Writer = discardWriter{}
