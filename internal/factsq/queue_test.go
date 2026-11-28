package factsq

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func nopACL(string) error { return nil }

func testQueue(t *testing.T, capBytes int64) *Queue {
	t.Helper()
	dir := t.TempDir()
	q := &Queue{Dir: dir, Cap: capBytes, ApplyACL: nopACL}
	if err := q.Open(); err != nil {
		t.Fatal(err)
	}
	return q
}

func batchJSON(streamID string, seq int64, payload string) []byte {
	return []byte(fmt.Sprintf(`{"stream_id":%q,"seq":%d,"payload":%q}`, streamID, seq, payload))
}

func TestDefaultCapBytes(t *testing.T) {
	t.Parallel()
	if DefaultCapBytes != 20<<20 {
		t.Fatalf("DefaultCapBytes = %d, want %d", DefaultCapBytes, 20<<20)
	}
}

func TestEnqueuePeekAckFIFO(t *testing.T) {
	t.Parallel()
	q := testQueue(t, 1<<20)
	stream := q.StreamID()

	for i := 1; i <= 3; i++ {
		data := batchJSON(stream, int64(i), fmt.Sprintf("b%d", i))
		seq, dropped, err := q.Enqueue(data)
		if err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
		if seq != int64(i) {
			t.Fatalf("seq = %d, want %d", seq, i)
		}
		if dropped.Batches != 0 || dropped.Bytes != 0 {
			t.Fatalf("unexpected drop: %+v", dropped)
		}
	}

	for i := 1; i <= 3; i++ {
		seq, data, ok, err := q.Peek()
		if err != nil || !ok {
			t.Fatalf("Peek %d: ok=%v err=%v", i, ok, err)
		}
		if seq != int64(i) {
			t.Fatalf("Peek seq = %d, want %d", seq, i)
		}
		want := batchJSON(stream, int64(i), fmt.Sprintf("b%d", i))
		if !bytes.Equal(data, want) {
			t.Fatalf("Peek data = %s, want %s", data, want)
		}
		if err := q.Ack(seq); err != nil {
			t.Fatalf("Ack %d: %v", i, err)
		}
	}

	_, _, ok, err := q.Peek()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected empty queue")
	}
}

func TestTmpFilesIgnoredAfterCrash(t *testing.T) {
	t.Parallel()
	q := testQueue(t, 1<<20)
	stream := q.StreamID()
	data := batchJSON(stream, 1, "ok")
	if _, _, err := q.Enqueue(data); err != nil {
		t.Fatal(err)
	}

	// Simulate crash between CreateTemp write and rename: leftover .tmp-* file.
	tmpPath := filepath.Join(q.Dir, ".tmp-crash-leftover")
	if err := os.WriteFile(tmpPath, []byte(`{"stream_id":"x","seq":99}`), 0o600); err != nil {
		t.Fatal(err)
	}

	seqs, err := q.PendingSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 1 || seqs[0] != 1 {
		t.Fatalf("PendingSeqs = %v, want [1]", seqs)
	}
	seq, got, ok, err := q.Peek()
	if err != nil || !ok || seq != 1 {
		t.Fatalf("Peek = seq=%d ok=%v err=%v", seq, ok, err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("data mismatch")
	}
}

func TestCrashBetweenRenameAndStateRecovery(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := &Queue{Dir: dir, Cap: 1 << 20, ApplyACL: nopACL}
	if err := q.Open(); err != nil {
		t.Fatal(err)
	}
	stream := q.StreamID()

	// Write batch file as if rename succeeded but state.json was not updated.
	batch := batchJSON(stream, 1, "orphaned")
	if err := os.WriteFile(filepath.Join(dir, "1.json"), batch, 0o600); err != nil {
		t.Fatal(err)
	}
	// state still says next_seq=1 (pre-enqueue).
	st := stateFile{StreamID: stream, NextSeq: 1}
	raw, _ := json.Marshal(st)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	q2 := &Queue{Dir: dir, Cap: 1 << 20, ApplyACL: nopACL}
	if err := q2.Open(); err != nil {
		t.Fatal(err)
	}
	// next_seq must advance past highest on-disk batch.
	if q2.NextSeq() != 2 {
		t.Fatalf("NextSeq = %d, want 2", q2.NextSeq())
	}
	if q2.StreamID() != stream {
		t.Fatalf("StreamID = %q, want %q", q2.StreamID(), stream)
	}
	seq, data, ok, err := q2.Peek()
	if err != nil || !ok || seq != 1 {
		t.Fatalf("Peek = %d ok=%v err=%v", seq, ok, err)
	}
	if !bytes.Equal(data, batch) {
		t.Fatal("batch bytes lost")
	}
}

func TestStateMissingCorruptOlderStreamReuse(t *testing.T) {
	t.Parallel()

	t.Run("missing_state_reuses_stream_from_batch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		stream := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
		batch := batchJSON(stream, 3, "keep")
		if err := os.MkdirAll(filepath.Join(dir, "rejected"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "3.json"), batch, 0o600); err != nil {
			t.Fatal(err)
		}
		// Documented recovery: when state.json is missing but batches remain,
		// stream_id is reused from batch metadata and next_seq = highest+1.
		q := &Queue{Dir: dir, Cap: 1 << 20, ApplyACL: nopACL}
		if err := q.Open(); err != nil {
			t.Fatal(err)
		}
		if q.StreamID() != stream {
			t.Fatalf("StreamID = %q, want reused %q", q.StreamID(), stream)
		}
		if q.NextSeq() != 4 {
			t.Fatalf("NextSeq = %d, want 4", q.NextSeq())
		}
	})

	t.Run("corrupt_state", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		stream := "11111111-2222-4333-8444-555555555555"
		if err := os.MkdirAll(filepath.Join(dir, "rejected"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "2.json"), batchJSON(stream, 2, "x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not-json"), 0o600); err != nil {
			t.Fatal(err)
		}
		q := &Queue{Dir: dir, Cap: 1 << 20, ApplyACL: nopACL}
		if err := q.Open(); err != nil {
			t.Fatal(err)
		}
		if q.StreamID() != stream {
			t.Fatalf("StreamID = %q, want %q", q.StreamID(), stream)
		}
		if q.NextSeq() != 3 {
			t.Fatalf("NextSeq = %d, want 3", q.NextSeq())
		}
	})

	t.Run("older_state_next_seq_bumped", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		stream := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		if err := os.MkdirAll(filepath.Join(dir, "rejected"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "5.json"), batchJSON(stream, 5, "late"), 0o600); err != nil {
			t.Fatal(err)
		}
		// state claims next_seq=2 but highest batch is 5 → max(state, highest+1)=6
		st, _ := json.Marshal(stateFile{StreamID: stream, NextSeq: 2})
		if err := os.WriteFile(filepath.Join(dir, "state.json"), st, 0o600); err != nil {
			t.Fatal(err)
		}
		q := &Queue{Dir: dir, Cap: 1 << 20, ApplyACL: nopACL}
		if err := q.Open(); err != nil {
			t.Fatal(err)
		}
		if q.NextSeq() != 6 {
			t.Fatalf("NextSeq = %d, want 6 (max of state and highest+1)", q.NextSeq())
		}
		if q.StreamID() != stream {
			t.Fatalf("StreamID = %q", q.StreamID())
		}
	})
}

func TestCorruptAndZeroByteBatchQuarantined(t *testing.T) {
	t.Parallel()
	q := testQueue(t, 1<<20)
	stream := q.StreamID()

	if err := os.WriteFile(q.batchPath(1), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(q.batchPath(2), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	good := batchJSON(stream, 3, "good")
	if err := os.WriteFile(q.batchPath(3), good, 0o600); err != nil {
		t.Fatal(err)
	}
	// Keep next_seq ahead so Open does not rewrite numbering.
	st, _ := json.Marshal(stateFile{StreamID: stream, NextSeq: 4})
	if err := os.WriteFile(q.statePath(), st, 0o600); err != nil {
		t.Fatal(err)
	}
	q.ready = false // force re-open recovery path on next call

	seq, data, ok, err := q.Peek()
	if err != nil || !ok {
		t.Fatalf("Peek: ok=%v err=%v", ok, err)
	}
	if seq != 3 || !bytes.Equal(data, good) {
		t.Fatalf("Peek = seq=%d data=%s", seq, data)
	}

	rejected := q.rejectedDir()
	for _, n := range []int64{1, 2} {
		path := filepath.Join(rejected, fmt.Sprintf("%d.json", n))
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected quarantined %s: %v", path, err)
		}
		if _, err := os.Stat(q.batchPath(n)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("batch %d still in queue dir", n)
		}
	}
}

func TestUnknownExtraFilesIgnored(t *testing.T) {
	t.Parallel()
	q := testQueue(t, 1<<20)
	stream := q.StreamID()
	good := batchJSON(stream, 1, "only")
	if _, _, err := q.Enqueue(good); err != nil {
		t.Fatal(err)
	}

	extras := []string{
		"notes.txt",
		"foo.json",
		"0.json",
		"-1.json",
		"1.json.bak",
		".hidden.json",
	}
	for _, name := range extras {
		if err := os.WriteFile(filepath.Join(q.Dir, name), []byte(`{"stream_id":"x"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	seqs, err := q.PendingSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if len(seqs) != 1 || seqs[0] != 1 {
		t.Fatalf("PendingSeqs = %v, want [1]", seqs)
	}
	seq, data, ok, err := q.Peek()
	if err != nil || !ok || seq != 1 || !bytes.Equal(data, good) {
		t.Fatalf("Peek seq=%d ok=%v data=%s err=%v", seq, ok, data, err)
	}
}

func TestCapOverflowDropsOldestAndErrFull(t *testing.T) {
	t.Parallel()

	t.Run("single_batch_larger_than_cap", func(t *testing.T) {
		t.Parallel()
		q := testQueue(t, 64)
		_, _, err := q.Enqueue(bytes.Repeat([]byte("x"), 65))
		if !errors.Is(err, ErrFull) {
			t.Fatalf("err = %v, want ErrFull", err)
		}
	})

	t.Run("overflow_drops_oldest_via_EnqueueBuild", func(t *testing.T) {
		t.Parallel()
		q := testQueue(t, 1<<20)
		stream := q.StreamID()
		mk := func(seq int64) []byte {
			return batchJSON(stream, seq, "x")
		}
		one := int64(len(mk(1)))
		// Cap fits exactly two batches; third enqueue must drop the oldest.
		q.Cap = one*2 + 1

		if _, _, err := q.Enqueue(mk(1)); err != nil {
			t.Fatal(err)
		}
		if _, _, err := q.Enqueue(mk(2)); err != nil {
			t.Fatal(err)
		}

		var seenDropped Dropped
		var builds int
		seq, dropped, err := q.EnqueueBuild(func(sid string, s int64, d Dropped) ([]byte, error) {
			builds++
			seenDropped = d
			if sid != stream {
				t.Fatalf("stream = %q", sid)
			}
			return mk(s), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if seq != 3 {
			t.Fatalf("seq = %d, want 3", seq)
		}
		if dropped.Batches != 1 || dropped.Bytes != one {
			t.Fatalf("dropped = %+v, want Batches=1 Bytes=%d", dropped, one)
		}
		if seenDropped != dropped {
			t.Fatalf("build saw %+v, returned %+v", seenDropped, dropped)
		}
		if builds < 2 {
			t.Fatalf("builds = %d, want ≥2 (retry after drop)", builds)
		}

		pending, err := q.PendingSeqs()
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 2 || pending[0] != 2 || pending[1] != 3 {
			t.Fatalf("PendingSeqs = %v, want [2 3]", pending)
		}
		size, err := q.SizeBytes()
		if err != nil {
			t.Fatal(err)
		}
		if size > q.Cap {
			t.Fatalf("SizeBytes = %d > Cap %d", size, q.Cap)
		}
	})
}

func TestConcurrentEnqueuePeekAck(t *testing.T) {
	t.Parallel()
	q := testQueue(t, 4<<20)
	stream := q.StreamID()

	const n = 50
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_, _, err := q.EnqueueBuild(func(sid string, seq int64, _ Dropped) ([]byte, error) {
				return batchJSON(sid, seq, fmt.Sprintf("c%d", i)), nil
			})
			if err != nil {
				t.Errorf("Enqueue: %v", err)
				return
			}
		}
	}()

	acked := 0
	go func() {
		defer wg.Done()
		for acked < n {
			seq, data, ok, err := q.Peek()
			if err != nil {
				t.Errorf("Peek: %v", err)
				return
			}
			if !ok {
				continue
			}
			if !json.Valid(data) {
				t.Errorf("invalid json for seq %d", seq)
				return
			}
			if stream == "" {
				t.Error("empty stream")
				return
			}
			if err := q.Ack(seq); err != nil {
				t.Errorf("Ack: %v", err)
				return
			}
			acked++
		}
	}()

	wg.Wait()
	_, _, ok, err := q.Peek()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("queue should be empty after concurrent drain")
	}
}

func TestRejectMovesToRejected(t *testing.T) {
	t.Parallel()
	q := testQueue(t, 1<<20)
	stream := q.StreamID()
	data := batchJSON(stream, 1, "bad")
	seq, _, err := q.Enqueue(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Reject(seq); err != nil {
		t.Fatal(err)
	}
	_, _, ok, err := q.Peek()
	if err != nil || ok {
		t.Fatalf("Peek after Reject: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(q.rejectedDir(), "1.json")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenEmptyDirMintsStream(t *testing.T) {
	t.Parallel()
	q := testQueue(t, 0) // Cap 0 → DefaultCapBytes
	if q.Cap != DefaultCapBytes {
		t.Fatalf("Cap = %d, want DefaultCapBytes", q.Cap)
	}
	if q.StreamID() == "" {
		t.Fatal("empty stream id")
	}
	if q.NextSeq() != 1 {
		t.Fatalf("NextSeq = %d, want 1", q.NextSeq())
	}
}

// failCreateTempFS returns err from CreateTemp (used for ENOSPC / EACCES injection).
type failCreateTempFS struct {
	OSFS
	err   error
	armed bool
}

func (f *failCreateTempFS) CreateTemp(dir, pattern string) (*os.File, error) {
	if f.armed {
		return nil, f.err
	}
	return f.OSFS.CreateTemp(dir, pattern)
}

func TestEnqueueENOSPCPreservesExisting(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	failFS := &failCreateTempFS{}
	q := &Queue{Dir: dir, Cap: 1 << 20, ApplyACL: nopACL, FS: failFS}
	if err := q.Open(); err != nil {
		t.Fatal(err)
	}
	stream := q.StreamID()
	first := batchJSON(stream, 1, "keep-me")
	if _, _, err := q.Enqueue(first); err != nil {
		t.Fatal(err)
	}
	failFS.armed = true
	failFS.err = errors.New("no space left on device")
	_, _, err := q.Enqueue(batchJSON(stream, 2, "new"))
	if err == nil {
		t.Fatal("expected ENOSPC-style enqueue error")
	}
	if !strings.Contains(err.Error(), "no space left on device") {
		t.Fatalf("err = %v", err)
	}
	failFS.armed = false
	seq, data, ok, err := q.Peek()
	if err != nil || !ok || seq != 1 {
		t.Fatalf("Peek = seq=%d ok=%v err=%v", seq, ok, err)
	}
	if !bytes.Equal(data, first) {
		t.Fatalf("existing batch corrupted: %s", data)
	}
	pending, err := q.PendingSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != 1 {
		t.Fatalf("PendingSeqs = %v, want [1]", pending)
	}
}

func TestEnqueueReadOnlyDirectory(t *testing.T) {
	t.Parallel()
	// Inject EACCES via FS — POSIX chmod 0555 does not deny writes on Windows.
	dir := t.TempDir()
	failFS := &failCreateTempFS{}
	q := &Queue{Dir: dir, Cap: 1 << 20, ApplyACL: nopACL, FS: failFS}
	if err := q.Open(); err != nil {
		t.Fatal(err)
	}
	stream := q.StreamID()
	if _, _, err := q.Enqueue(batchJSON(stream, 1, "before-ro")); err != nil {
		t.Fatal(err)
	}
	failFS.armed = true
	failFS.err = os.ErrPermission

	_, _, err := q.Enqueue(batchJSON(stream, 2, "after-ro"))
	if err == nil {
		t.Fatal("expected enqueue error on read-only directory")
	}
	if !errors.Is(err, os.ErrPermission) && !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v, want permission error", err)
	}
	failFS.armed = false
	pending, err := q.PendingSeqs()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0] != 1 {
		t.Fatalf("PendingSeqs = %v, want [1] (existing intact)", pending)
	}
}
