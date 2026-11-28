package ipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	payloads := [][]byte{
		{},
		[]byte("x"),
		[]byte(`{"type":"ping"}`),
		bytes.Repeat([]byte("a"), MaxFrameBytes),
	}
	for i, p := range payloads {
		var buf bytes.Buffer
		if err := WriteFrame(&buf, p); err != nil {
			t.Fatalf("case %d write: %v", i, err)
		}
		got, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("case %d read: %v", i, err)
		}
		if !bytes.Equal(got, p) {
			t.Fatalf("case %d mismatch len=%d want=%d", i, len(got), len(p))
		}
	}
}

func TestWriteRejectsOverMax(t *testing.T) {
	err := WriteFrame(io.Discard, bytes.Repeat([]byte("x"), MaxFrameBytes+1))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err=%v want ErrFrameTooLarge", err)
	}
}

func TestReadRejectsOversizeLengthPrefix(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(MaxFrameBytes+1))
	// Append huge claimed payload that must NOT be fully read/allocated.
	r := io.MultiReader(bytes.NewReader(hdr[:]), strings.NewReader(strings.Repeat("x", 1024)))
	got, err := ReadFrame(r)
	if got != nil {
		t.Fatalf("allocated %d bytes on oversize", len(got))
	}
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err=%v want ErrFrameTooLarge", err)
	}
}

func TestReadRejectsMaxUint32Length(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 0xFFFFFFFF)
	_, err := ReadFrame(bytes.NewReader(hdr[:]))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err=%v want ErrFrameTooLarge", err)
	}
}

func TestReadTruncatedHeader(t *testing.T) {
	_, err := ReadFrame(bytes.NewReader([]byte{0, 0, 1}))
	if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Fatalf("err=%v want EOF/unexpected", err)
	}
}

func TestReadTruncatedPayload(t *testing.T) {
	var buf bytes.Buffer
	_ = WriteFrame(&buf, []byte("hello-world"))
	 partial := buf.Bytes()[:6] // header + 2 bytes
	_, err := ReadFrame(bytes.NewReader(partial))
	if err == nil {
		t.Fatal("expected truncated payload error")
	}
	if !errors.Is(err, ErrBadFrame) && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v", err)
	}
}

type byteAtATime struct {
	r   *bytes.Reader
	n   int
	max int
}

func (b *byteAtATime) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	var one [1]byte
	n, err := b.r.Read(one[:])
	if n == 1 {
		p[0] = one[0]
		b.n++
		if b.n > b.max {
			b.max = b.n
		}
	}
	return n, err
}

func TestSlowWriterReassembles(t *testing.T) {
	var src bytes.Buffer
	payload := []byte(`{"type":"pong"}`)
	if err := WriteFrame(&src, payload); err != nil {
		t.Fatal(err)
	}
	r := &byteAtATime{r: bytes.NewReader(src.Bytes())}
	got, err := ReadFrame(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q", got)
	}
}

func TestTwoFramesOneBuffer(t *testing.T) {
	var buf bytes.Buffer
	_ = WriteFrame(&buf, []byte(`{"type":"ping"}`))
	_ = WriteFrame(&buf, []byte(`{"type":"pong"}`))
	a, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ReadFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != `{"type":"ping"}` || string(b) != `{"type":"pong"}` {
		t.Fatalf("a=%s b=%s", a, b)
	}
}

func TestReadNeverAllocatesDeclaredOversize(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 0x7FFFFFFF)
	alloc := 0
	r := &allocTrackingReader{r: bytes.NewReader(hdr[:]), onRead: func(n int) { alloc += n }}
	_, err := ReadFrame(r)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("err=%v", err)
	}
	if alloc > 4 {
		t.Fatalf("read %d bytes after oversize header; want only header", alloc)
	}
}

type allocTrackingReader struct {
	r      io.Reader
	onRead func(int)
}

func (a *allocTrackingReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if a.onRead != nil {
		a.onRead(n)
	}
	return n, err
}
