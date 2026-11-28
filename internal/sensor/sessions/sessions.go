// Package sessions implements the interactive-session sensor: session.state and
// proc.sample facts per logged-on user.
package sessions

import (
	"context"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/sensor"
	"github.com/barzoj/dad-joke/internal/winsess"
)

const (
	pluginID      = "sessions"
	maxExes       = 200
	maxExeRunes   = 260
	defaultDurSec = 60
)

// well-known service SIDs excluded from proc.sample.
var excludedOwnerSIDs = map[string]struct{}{
	"S-1-5-18": {}, // LocalSystem
	"S-1-5-19": {}, // LocalService
	"S-1-5-20": {}, // NetworkService
}

// Sensor emits session.state + proc.sample facts.
type Sensor struct {
	Source   winsess.Source
	Interval time.Duration // used for first tick and clamp bounds

	mu       sync.Mutex
	prevSample time.Time
}

// New returns a sessions Sensor with the given source and sample interval.
func New(src winsess.Source, interval time.Duration) *Sensor {
	if src == nil {
		src = winsess.Default()
	}
	if interval < time.Second {
		interval = time.Duration(defaultDurSec) * time.Second
	}
	return &Sensor{Source: src, Interval: interval}
}

// Reset clears the previous-sample timestamp so the next dur_s does not span
// sleep/resume (or a wall-clock jump).
func (s *Sensor) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prevSample = time.Time{}
}

// Collect implements sensor.Sensor.
func (s *Sensor) Collect(ctx context.Context, now time.Time) ([]sensor.Fact, error) {
	if s.Source == nil {
		s.Source = winsess.Default()
	}
	sessions, err := s.Source.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	procs, err := s.Source.Processes(ctx)
	if err != nil {
		return nil, err
	}

	dur := s.nextDur(now)
	bySID := indexUserProcs(procs)

	var out []sensor.Fact
	for _, sess := range sessions {
		if sess.UserName == "" && sess.UserSID == "" {
			// Skip services session / listener without a user.
			continue
		}
		sid := sess.UserSID
		state := agentapi.DadJokeCommonFactSessionState(sess.State)
		if !state.Valid() {
			state = agentapi.Active
		}
		sessionID := strconv.FormatUint(uint64(sess.ID), 10)
		uname := sess.UserName
		st := state
		fact := sensor.Fact{
			Ts:     now.UTC(),
			Kind:   agentapi.SessionState,
			Plugin: pluginID,
			DurS:   dur,
			Data: agentapi.FactData{
				SessionId: &sessionID,
				Username:  &uname,
				State:     &st,
			},
		}
		if sid != "" {
			fact.UserSid = &sid
		}
		out = append(out, fact)

		exes := bySID[sid]
		if exes == nil {
			exes = []string{}
		}
		exesCopy := append([]string(nil), exes...)
		proc := sensor.Fact{
			Ts:     now.UTC(),
			Kind:   agentapi.ProcSample,
			Plugin: pluginID,
			DurS:   dur,
			Data:   agentapi.FactData{Exes: &exesCopy},
		}
		if sid != "" {
			proc.UserSid = &sid
		}
		out = append(out, proc)
	}
	return out, nil
}

func (s *Sensor) nextDur(now time.Time) int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	interval := s.Interval
	if interval < time.Second {
		interval = time.Duration(defaultDurSec) * time.Second
	}
	intervalSec := int32(interval / time.Second)
	if intervalSec < 1 {
		intervalSec = 1
	}
	// Contract FactDurMaxSeconds = 300; never emit dur_s above that or the
	// backend 400s the whole batch into rejected/.
	maxDur := intervalSec * 2
	if maxDur > 300 {
		maxDur = 300
	}
	if s.prevSample.IsZero() {
		s.prevSample = now
		return intervalSec
	}
	delta := now.Sub(s.prevSample)
	// Wall-clock jump after sleep / clock change: drop previous timestamp (AC #6).
	if delta > 2*interval || delta < 0 {
		s.prevSample = now
		return intervalSec
	}
	s.prevSample = now
	sec64 := int64(delta / time.Second)
	if sec64 < 1 {
		sec64 = 1
	}
	if sec64 > int64(maxDur) {
		sec64 = int64(maxDur)
	}
	return int32(sec64) // #nosec G115 -- clamped to maxDur (≤300) above
}

func indexUserProcs(procs []winsess.Process) map[string][]string {
	tmp := make(map[string]map[string]struct{})
	for _, p := range procs {
		if p.OwnerSID == "" {
			continue
		}
		if _, skip := excludedOwnerSIDs[p.OwnerSID]; skip {
			continue
		}
		name := basename(p.Exe)
		if name == "" {
			continue
		}
		if utf8.RuneCountInString(name) > maxExeRunes {
			name = string([]rune(name)[:maxExeRunes])
		}
		set, ok := tmp[p.OwnerSID]
		if !ok {
			set = make(map[string]struct{})
			tmp[p.OwnerSID] = set
		}
		set[name] = struct{}{}
	}
	out := make(map[string][]string, len(tmp))
	for sid, set := range tmp {
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		if len(names) > maxExes {
			names = names[:maxExes]
		}
		out[sid] = names
	}
	return out
}

func basename(exe string) string {
	// Normalize Windows separators so Linux unit tests strip paths correctly.
	exe = strings.ReplaceAll(exe, `\`, `/`)
	base := filepath.Base(exe)
	if base == "." || base == "/" || base == "" {
		return ""
	}
	return base
}
