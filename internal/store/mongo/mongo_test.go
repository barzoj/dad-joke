package mongo

import (
	"context"
	"testing"
	"time"
)

func TestOpen_RequiresURI(t *testing.T) {
	t.Parallel()
	_, err := Open(context.Background(), Config{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPing_NilStore(t *testing.T) {
	t.Parallel()
	var st *Store
	if err := st.Ping(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if err := st.Close(context.Background()); err != nil {
		t.Fatalf("Close nil: %v", err)
	}
}

func TestPingContext_PreservesCallerDeadline(t *testing.T) {
	t.Parallel()
	parent, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, stop := pingContext(parent, 10*time.Second)
	defer stop()
	if got != parent {
		t.Fatal("pingContext must reuse caller context when a deadline is set")
	}
	deadline, ok := got.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	if rem := time.Until(deadline); rem < 20*time.Second {
		t.Fatalf("deadline truncated: remaining=%s", rem)
	}
}

func TestPingContext_AppliesDefaultWithoutDeadline(t *testing.T) {
	t.Parallel()
	got, stop := pingContext(context.Background(), 10*time.Second)
	defer stop()
	deadline, ok := got.Deadline()
	if !ok {
		t.Fatal("expected default deadline")
	}
	rem := time.Until(deadline)
	if rem < 8*time.Second || rem > 10*time.Second {
		t.Fatalf("default deadline remaining=%s, want ~10s", rem)
	}
}

func TestIsTransientMongoOpenError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		msg  string
		want bool
	}{
		{"mongo: ping: server selection timeout", true},
		{"connection refused", true},
		{"authentication failed", false},
		{"", false},
	}
	for _, tc := range cases {
		err := error(nil)
		if tc.msg != "" {
			err = errString(tc.msg)
		}
		if got := isTransientMongoOpenError(err); got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.msg, got, tc.want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
