package ipc

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
)

// ErrFrameTooLarge is returned when a length prefix exceeds MaxFrameBytes.
var ErrFrameTooLarge = errors.New("ipc: frame too large")

// ErrBadFrame is returned for truncated or otherwise unreadable frames.
var ErrBadFrame = errors.New("ipc: bad frame")

// WriteFrame writes a length-prefixed payload. Rejects payloads > MaxFrameBytes.
func WriteFrame(w io.Writer, payload []byte) error {
	n, err := frameLen(len(payload))
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], n)
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err = w.Write(payload)
	return err
}

// frameLen converts a payload length to a uint32 length prefix after bounds checks.
func frameLen(n int) (uint32, error) {
	if n < 0 || n > MaxFrameBytes {
		return 0, ErrFrameTooLarge
	}
	if n > math.MaxUint32 {
		return 0, ErrFrameTooLarge
	}
	return uint32(n), nil
}

// ReadFrame reads one length-prefixed payload. It never allocates more than
// MaxFrameBytes for the payload. A length prefix > MaxFrameBytes returns
// ErrFrameTooLarge without allocating the declared size.
func ReadFrame(r io.Reader) ([]byte, error) {
	return ReadFrameLimited(r, MaxFrameBytes)
}

// ReadFrameLimited is like ReadFrame but with an explicit limit (for tests).
func ReadFrameLimited(r io.Reader, limit int) ([]byte, error) {
	if limit <= 0 || limit > MaxFrameBytes {
		limit = MaxFrameBytes
	}
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: header: %v", ErrBadFrame, err)
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if int64(n) > int64(limit) {
		return nil, fmt.Errorf("%w: declared %d > limit %d", ErrFrameTooLarge, n, limit)
	}
	if n == 0 {
		return []byte{}, nil
	}
	buf := make([]byte, int(n))
	if _, err := io.ReadFull(r, buf); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("%w: truncated payload: %w", ErrBadFrame, err)
		}
		return nil, fmt.Errorf("%w: payload: %v", ErrBadFrame, err)
	}
	return buf, nil
}
