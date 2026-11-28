package ipc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeEncodeHello(t *testing.T) {
	m := NewHello(ProtocolVersion, "0.0.1-dev", 3, 4242)
	b, err := EncodeMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMessage(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeHello || got.Protocol != ProtocolVersion || got.SessionID != 3 || got.PID != 4242 {
		t.Fatalf("%+v", got)
	}
}

func TestDecodeMalformed(t *testing.T) {
	cases := []string{
		"",
		"{",
		`{"type":}`,
		`[]`,
		`{"type":1}`,
		`{"nope":true}`,
	}
	for _, c := range cases {
		_, err := DecodeMessage([]byte(c))
		if err == nil {
			t.Fatalf("expected error for %q", c)
		}
	}
}

func TestUnknownTypePreserved(t *testing.T) {
	raw := []byte(`{"type":"future_thing","protocol":1}`)
	m, err := DecodeMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != "future_thing" {
		t.Fatalf("%q", m.Type)
	}
}

func TestHelloAckFields(t *testing.T) {
	m := NewHelloAck(1, 60, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	b, _ := EncodeMessage(m)
	if !bytes.Contains(b, []byte(`"interval_seconds":60`)) {
		t.Fatalf("%s", b)
	}
	if !strings.Contains(string(b), "2026-01-02") {
		t.Fatalf("%s", b)
	}
}

func TestMsgEnvelope(t *testing.T) {
	m := NewMsg("fg.sample", json.RawMessage(`{"exe":"x"}`))
	b, err := EncodeMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeMessage(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeMsg || got.MsgType != "fg.sample" {
		t.Fatalf("%+v", got)
	}
}

func TestPipeName(t *testing.T) {
	if PipeName(7) != `\\.\pipe\DadJoke.session.7` {
		t.Fatal(PipeName(7))
	}
	id, err := ParsePipeSessionID(PipeName(7))
	if err != nil || id != 7 {
		t.Fatalf("%d %v", id, err)
	}
}

func TestReconnectBackoff(t *testing.T) {
	var b ReconnectBackoff
	var seq []time.Duration
	for i := 0; i < 8; i++ {
		seq = append(seq, b.Next())
	}
	if seq[0] != 250*time.Millisecond {
		t.Fatalf("first=%v", seq[0])
	}
	if seq[len(seq)-1] != 5*time.Second {
		t.Fatalf("capped=%v", seq[len(seq)-1])
	}
	b.Reset()
	if b.Next() != 250*time.Millisecond {
		t.Fatal("reset failed")
	}
}

func TestRateLimiterBounds(t *testing.T) {
	now := time.Unix(0, 0)
	r := NewRateLimiter(time.Second)
	r.Clock = func() time.Time { return now }
	ok, _ := r.Allow()
	if !ok {
		t.Fatal("first should allow")
	}
	for i := 0; i < 10000; i++ {
		ok, _ = r.Allow()
		if ok {
			t.Fatalf("allowed at iteration %d", i)
		}
	}
	now = now.Add(time.Second)
	ok, suppressed := r.Allow()
	if !ok || suppressed != 10000 {
		t.Fatalf("ok=%v suppressed=%d", ok, suppressed)
	}
}
