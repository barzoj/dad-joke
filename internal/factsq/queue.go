// Package factsq is the durable, bounded facts batch queue under
// ProgramData\DadJoke\facts\. Batches are immutable bytes; seq is assigned at
// enqueue and never reused.
package factsq

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/barzoj/dad-joke/internal/acl"
	"github.com/google/uuid"
)

// DefaultCapBytes is the on-disk queue size cap (20 MiB).
const DefaultCapBytes = 20 << 20

// ErrFull is returned when a single batch cannot fit even after dropping oldest.
var ErrFull = errors.New("factsq: batch exceeds queue capacity")

// Dropped describes batches removed by the overflow policy.
type Dropped struct {
	Batches int
	Bytes   int64
}

// Queue is a durable FIFO of immutable batch files.
type Queue struct {
	Dir string
	Cap int64 // bytes; 0 → DefaultCapBytes

	// ApplyACL sets SYSTEM+Administrators ACL; nil uses acl.Apply.
	ApplyACL func(path string) error

	mu       sync.Mutex
	streamID string
	nextSeq  int64
	ready    bool
}

type stateFile struct {
	StreamID string `json:"stream_id"`
	NextSeq  int64  `json:"next_seq"`
}

// Open initializes the queue directory, recovers stream_id/next_seq, and
// applies the data ACL. Safe to call multiple times.
func (q *Queue) Open() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.openLocked()
}

func (q *Queue) openLocked() error {
	if q.ready {
		return nil
	}
	if q.Dir == "" {
		return fmt.Errorf("factsq: empty Dir")
	}
	if q.Cap <= 0 {
		q.Cap = DefaultCapBytes
	}
	if q.ApplyACL == nil {
		q.ApplyACL = acl.Apply
	}
	if err := os.MkdirAll(q.Dir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(q.rejectedDir(), 0o700); err != nil {
		return err
	}
	_ = q.ApplyACL(q.Dir)
	_ = q.ApplyACL(q.rejectedDir())

	stream, next, err := q.recoverState()
	if err != nil {
		return err
	}
	q.streamID = stream
	q.nextSeq = next
	q.ready = true
	return nil
}

func (q *Queue) rejectedDir() string {
	return filepath.Join(q.Dir, "rejected")
}

func (q *Queue) statePath() string {
	return filepath.Join(q.Dir, "state.json")
}

func (q *Queue) batchPath(seq int64) string {
	return filepath.Join(q.Dir, fmt.Sprintf("%d.json", seq))
}

// StreamID returns the persistent stream id (after Open).
func (q *Queue) StreamID() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.openLocked()
	return q.streamID
}

// NextSeq returns the next seq that will be assigned (after Open).
func (q *Queue) NextSeq() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	_ = q.openLocked()
	return q.nextSeq
}

// Enqueue writes data as the next batch. Overflow drops oldest batches first.
// Prefer EnqueueBuild when the payload must embed the assigned seq / drops.
func (q *Queue) Enqueue(data []byte) (seq int64, dropped Dropped, err error) {
	return q.EnqueueBuild(func(string, int64, Dropped) ([]byte, error) { return data, nil })
}

// EnqueueBuild assigns seq and asks build for immutable batch bytes. When the
// queue is over capacity, oldest batches are dropped and build is called again
// with the cumulative Dropped counts so the batch can embed queue.dropped.
func (q *Queue) EnqueueBuild(build func(streamID string, seq int64, dropped Dropped) ([]byte, error)) (seq int64, dropped Dropped, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.openLocked(); err != nil {
		return 0, Dropped{}, err
	}
	seq = q.nextSeq
	for {
		data, berr := build(q.streamID, seq, dropped)
		if berr != nil {
			return 0, Dropped{}, berr
		}
		if int64(len(data)) > q.Cap {
			return 0, Dropped{}, ErrFull
		}
		total, serr := q.sizeLocked()
		if serr != nil {
			return 0, Dropped{}, serr
		}
		if total+int64(len(data)) <= q.Cap {
			path := q.batchPath(seq)
			if err := atomicWriteFile(path, data, q.ApplyACL); err != nil {
				return 0, dropped, err
			}
			q.nextSeq = seq + 1
			if err := q.writeStateLocked(); err != nil {
				return seq, dropped, err
			}
			return seq, dropped, nil
		}
		d, derr := q.dropOldestLocked()
		if derr != nil {
			return 0, Dropped{}, derr
		}
		if d.Batches == 0 {
			return 0, Dropped{}, ErrFull
		}
		dropped.Batches += d.Batches
		dropped.Bytes += d.Bytes
	}
}

// Rewrite replaces an existing batch file's bytes (same seq). Used when a
// producer must correct the payload after assignment; prefer EnqueueBuild.
func (q *Queue) Rewrite(seq int64, data []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.openLocked(); err != nil {
		return err
	}
	return atomicWriteFile(q.batchPath(seq), data, q.ApplyACL)
}

// Peek returns the oldest pending batch bytes and seq without removing it.
// ok is false when the queue is empty.
func (q *Queue) Peek() (seq int64, data []byte, ok bool, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.openLocked(); err != nil {
		return 0, nil, false, err
	}
	seqs, err := q.listBatchSeqsLocked()
	if err != nil {
		return 0, nil, false, err
	}
	for {
		if len(seqs) == 0 {
			return 0, nil, false, nil
		}
		seq = seqs[0]
		path := q.batchPath(seq)
		data, err = os.ReadFile(path) // #nosec G304 -- path under controlled Dir
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				seqs = seqs[1:]
				continue
			}
			return 0, nil, false, err
		}
		if len(data) == 0 || !json.Valid(data) {
			_ = q.quarantineLocked(seq, path)
			seqs = seqs[1:]
			continue
		}
		return seq, data, true, nil
	}
}

// Ack deletes the batch at seq after a successful upload (incl. duplicate).
func (q *Queue) Ack(seq int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.openLocked(); err != nil {
		return err
	}
	path := q.batchPath(seq)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Reject moves a batch to rejected\ (400/413) and continues.
func (q *Queue) Reject(seq int64) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.openLocked(); err != nil {
		return err
	}
	return q.quarantineLocked(seq, q.batchPath(seq))
}

func (q *Queue) quarantineLocked(seq int64, path string) error {
	dest := filepath.Join(q.rejectedDir(), fmt.Sprintf("%d.json", seq))
	if err := os.Rename(path, dest); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		// Cross-device or existing dest: copy+remove best effort.
		data, rerr := os.ReadFile(path) // #nosec G304
		if rerr != nil {
			return rerr
		}
		if werr := atomicWriteFile(dest, data, q.ApplyACL); werr != nil {
			return werr
		}
		_ = os.Remove(path)
	}
	_ = q.ApplyACL(dest)
	return nil
}

func (q *Queue) recoverState() (streamID string, nextSeq int64, err error) {
	seqs, err := q.listBatchSeqsLocked()
	if err != nil {
		return "", 0, err
	}
	highest := int64(0)
	streamFromBatch := ""
	for _, seq := range seqs {
		if seq > highest {
			highest = seq
		}
		if streamFromBatch == "" {
			if id := readStreamID(q.batchPath(seq)); id != "" {
				streamFromBatch = id
			}
		}
	}

	st, ok := q.readStateFile()
	switch {
	case ok && st.StreamID != "":
		streamID = st.StreamID
		nextSeq = st.NextSeq
	case streamFromBatch != "":
		// state.json missing/corrupt but batches remain: reuse stream from
		// batch metadata rather than minting a colliding seq space.
		streamID = streamFromBatch
		nextSeq = highest + 1
	default:
		streamID = uuid.NewString()
		nextSeq = 1
	}
	if highest+1 > nextSeq {
		nextSeq = highest + 1
	}
	if nextSeq < 1 {
		nextSeq = 1
	}
	q.streamID = streamID
	q.nextSeq = nextSeq
	if err := q.writeStateLocked(); err != nil {
		return "", 0, err
	}
	return streamID, nextSeq, nil
}

func (q *Queue) readStateFile() (stateFile, bool) {
	raw, err := os.ReadFile(q.statePath()) // #nosec G304
	if err != nil {
		return stateFile{}, false
	}
	var st stateFile
	if err := json.Unmarshal(raw, &st); err != nil {
		return stateFile{}, false
	}
	if st.StreamID == "" || st.NextSeq < 1 {
		return stateFile{}, false
	}
	return st, true
}

func (q *Queue) writeStateLocked() error {
	st := stateFile{StreamID: q.streamID, NextSeq: q.nextSeq}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return atomicWriteFile(q.statePath(), raw, q.ApplyACL)
}

func (q *Queue) listBatchSeqsLocked() ([]int64, error) {
	entries, err := os.ReadDir(q.Dir)
	if err != nil {
		return nil, err
	}
	var seqs []int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || name == "state.json" {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimSuffix(name, ".json"), 10, 64)
		if err != nil || n < 1 {
			continue // unknown extra files ignored
		}
		seqs = append(seqs, n)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs, nil
}

func (q *Queue) sizeLocked() (int64, error) {
	seqs, err := q.listBatchSeqsLocked()
	if err != nil {
		return 0, err
	}
	var total int64
	for _, seq := range seqs {
		fi, err := os.Stat(q.batchPath(seq))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return 0, err
		}
		total += fi.Size()
	}
	return total, nil
}

func (q *Queue) dropOldestLocked() (Dropped, error) {
	seqs, err := q.listBatchSeqsLocked()
	if err != nil {
		return Dropped{}, err
	}
	if len(seqs) == 0 {
		return Dropped{}, nil
	}
	seq := seqs[0]
	path := q.batchPath(seq)
	fi, err := os.Stat(path)
	var size int64
	if err == nil {
		size = fi.Size()
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Dropped{}, err
	}
	return Dropped{Batches: 1, Bytes: size}, nil
}

func readStreamID(path string) string {
	raw, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return ""
	}
	var meta struct {
		StreamID string `json:"stream_id"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ""
	}
	return meta.StreamID
}

func atomicWriteFile(path string, data []byte, applyACL func(string) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	if applyACL != nil {
		_ = applyACL(path)
	}
	return nil
}

// SizeBytes returns the total size of pending batch files (tests / diagnostics).
func (q *Queue) SizeBytes() (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.openLocked(); err != nil {
		return 0, err
	}
	return q.sizeLocked()
}

// PendingSeqs returns sorted pending batch sequence numbers.
func (q *Queue) PendingSeqs() ([]int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := q.openLocked(); err != nil {
		return nil, err
	}
	return q.listBatchSeqsLocked()
}

// IsNotExist reports whether err is a missing-path error.
func IsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
