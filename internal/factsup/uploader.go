// Package factsup uploads durable fact batches from factsq to AgentAPI
// POST /v1/facts with retry/backoff. Implements service.Agent.
package factsup

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/barzoj/dad-joke/gen/go/agentapi"
	"github.com/barzoj/dad-joke/internal/config"
	"github.com/barzoj/dad-joke/internal/factsq"
	"github.com/barzoj/dad-joke/internal/installer"
)

const (
	defaultHTTPTimeout = 30 * time.Second
	maxHTTPRedirects   = 10
	maxBackoff         = 5 * time.Minute
	baseBackoff        = time.Second
	idlePoll           = time.Second
)

// Logger is a minimal log sink.
type Logger interface {
	Log(format string, args ...any)
}

// NopLogger discards logs.
type NopLogger struct{}

// Log implements Logger.
func (NopLogger) Log(string, ...any) {}

// WriterLogger logs to an io.Writer.
type WriterLogger struct{ W io.Writer }

// Log implements Logger.
func (l WriterLogger) Log(format string, args ...any) {
	if l.W == nil {
		return
	}
	_, _ = fmt.Fprintf(l.W, format+"\n", args...)
}

// Clock abstracts time for tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// RealClock uses the wall clock.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now() }

// After implements Clock.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Queue is the subset of factsq.Queue used by the uploader.
type Queue interface {
	Peek() (seq int64, data []byte, ok bool, err error)
	Ack(seq int64) error
	Reject(seq int64) error
}

// HTTPDoer posts raw batch bytes (immutable retry).
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Loop uploads queued batches until ctx is cancelled.
type Loop struct {
	ConfigPath string
	Config     *config.Config
	Queue      Queue
	Log        Logger
	Clock      Clock
	HTTP       HTTPDoer
	NewHTTP    func(backendURL string) (HTTPDoer, error)

	// InFlight is 1 while a request is outstanding (tests).
	InFlight atomic.Int32

	backoff time.Duration
	rng     *rand.Rand
}

// New returns a production uploader wired to default paths and queue dir.
func New(q Queue) *Loop {
	paths := installer.DefaultPaths()
	if q == nil {
		q = &factsq.Queue{Dir: filepath.Join(paths.DataDir, "facts")}
	}
	return &Loop{
		ConfigPath: paths.ConfigPath,
		Queue:      q,
		Log:        WriterLogger{W: os.Stderr},
		Clock:      RealClock{},
	}
}

// Run implements service.Agent.
func (l *Loop) Run(ctx context.Context) error {
	if l.Log == nil {
		l.Log = NopLogger{}
	}
	if l.Clock == nil {
		l.Clock = RealClock{}
	}
	if l.rng == nil {
		l.rng = rand.New(rand.NewSource(l.Clock.Now().UnixNano())) // #nosec G404 -- jitter only
	}
	if l.ConfigPath == "" {
		l.ConfigPath = installer.DefaultPaths().ConfigPath
	}
	if l.NewHTTP == nil {
		l.NewHTTP = l.defaultHTTP
	}
	if l.Queue == nil {
		paths := installer.DefaultPaths()
		l.Queue = &factsq.Queue{Dir: filepath.Join(paths.DataDir, "facts")}
	}

	cfg, err := l.loadConfig()
	if err != nil {
		l.Log.Log("factsup: load config failed (idle until stop): %v", err)
		return l.idle(ctx)
	}
	if strings.TrimSpace(cfg.BackendURL) == "" {
		l.Log.Log("factsup: backend_url unset; uploader idle")
		return l.idle(ctx)
	}

	client, err := l.NewHTTP(cfg.BackendURL)
	if err != nil {
		l.Log.Log("factsup: client init failed (idle until stop): %s", redact(err.Error(), cfg.Token))
		return l.idle(ctx)
	}
	clientBackend := cfg.BackendURL
	l.backoff = 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, loadErr := l.loadConfig()
		if loadErr != nil {
			l.Log.Log("factsup: reload config failed (continuing): %v", loadErr)
		} else {
			cfg = next
			backend := strings.TrimSpace(cfg.BackendURL)
			if backend == "" {
				l.Log.Log("factsup: backend_url cleared; uploader idle")
				return l.idle(ctx)
			}
			if backend != clientBackend {
				rebuilt, cerr := l.NewHTTP(backend)
				if cerr != nil {
					l.Log.Log("factsup: client rebuild failed (continuing): %s", redact(cerr.Error(), cfg.Token))
				} else {
					client = rebuilt
					clientBackend = backend
				}
			}
		}

		seq, data, ok, perr := l.Queue.Peek()
		if perr != nil {
			l.Log.Log("factsup: peek failed: %v", perr)
			if err := l.wait(ctx, idlePoll); err != nil {
				return err
			}
			continue
		}
		if !ok {
			if err := l.wait(ctx, idlePoll); err != nil {
				return err
			}
			continue
		}

		status, retryAfter, err := l.postOnce(ctx, client, cfg, data)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			l.Log.Log("factsup: upload seq=%d failed (retry): %s", seq, redact(err.Error(), cfg.Token))
			if err := l.waitBackoff(ctx, retryAfter); err != nil {
				return err
			}
			continue
		}
		switch status {
		case http.StatusOK:
			if err := l.Queue.Ack(seq); err != nil {
				l.Log.Log("factsup: ack seq=%d failed: %v", seq, err)
			}
			l.backoff = 0
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge:
			if err := l.Queue.Reject(seq); err != nil {
				l.Log.Log("factsup: reject seq=%d failed: %v", seq, err)
			}
			l.backoff = 0
			l.Log.Log("factsup: seq=%d rejected (HTTP %d)", seq, status)
		case http.StatusUnauthorized, http.StatusForbidden:
			l.Log.Log("factsup: auth failed HTTP %d (batch retained, backing off)", status)
			if err := l.waitBackoff(ctx, retryAfter); err != nil {
				return err
			}
		case http.StatusTooManyRequests:
			if err := l.waitBackoff(ctx, retryAfter); err != nil {
				return err
			}
		default:
			// 5xx and unexpected — retry same bytes.
			l.Log.Log("factsup: HTTP %d for seq=%d (retry)", status, seq)
			if err := l.waitBackoff(ctx, retryAfter); err != nil {
				return err
			}
		}
	}
}

func (l *Loop) postOnce(ctx context.Context, client HTTPDoer, cfg config.Config, data []byte) (status int, retryAfter time.Duration, err error) {
	l.InFlight.Add(1)
	defer l.InFlight.Add(-1)

	u, err := url.JoinPath(strings.TrimRight(cfg.BackendURL, "/"), "/v1/facts")
	if err != nil {
		return 0, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	ra := parseRetryAfter(resp.Header.Get("Retry-After"), l.Clock.Now())
	return resp.StatusCode, ra, nil
}

func (l *Loop) waitBackoff(ctx context.Context, retryAfter time.Duration) error {
	d := l.nextBackoff()
	if retryAfter > d {
		d = retryAfter
	}
	return l.wait(ctx, d)
}

func (l *Loop) nextBackoff() time.Duration {
	if l.backoff <= 0 {
		l.backoff = baseBackoff
	} else {
		l.backoff *= 2
		if l.backoff > maxBackoff {
			l.backoff = maxBackoff
		}
	}
	// Full jitter in [0, backoff].
	j := time.Duration(0)
	if l.rng != nil && l.backoff > 0 {
		j = time.Duration(l.rng.Int63n(int64(l.backoff) + 1))
	}
	return j
}

func (l *Loop) wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		d = idlePoll
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.Clock.After(d):
		return nil
	}
}

func (l *Loop) idle(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (l *Loop) loadConfig() (config.Config, error) {
	if l.Config != nil {
		if err := l.Config.Validate(); err != nil {
			return config.Config{}, err
		}
		return *l.Config, nil
	}
	return config.LoadFile(l.ConfigPath)
}

func (l *Loop) defaultHTTP(backendURL string) (HTTPDoer, error) {
	if err := config.ValidateBackendURL(backendURL); err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout:       defaultHTTPTimeout,
		CheckRedirect: checkRedirect,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConns:          4,
			IdleConnTimeout:       30 * time.Second,
		},
	}, nil
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxHTTPRedirects {
		return fmt.Errorf("stopped after %d redirects", maxHTTPRedirects)
	}
	if req.URL == nil {
		return fmt.Errorf("redirect missing url")
	}
	if err := config.ValidateBackendURL(req.URL.String()); err != nil {
		return fmt.Errorf("redirect blocked: %w", err)
	}
	if len(via) > 0 && via[0].URL != nil {
		if !sameOrigin(via[0].URL, req.URL) {
			req.Header.Del("Authorization")
		}
	}
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(a.Scheme), strings.TrimSpace(b.Scheme)) &&
		strings.EqualFold(strings.TrimSpace(a.Host), strings.TrimSpace(b.Host))
}

func parseRetryAfter(h string, now time.Time) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	if sec, err := strconv.Atoi(h); err == nil {
		if sec < 0 {
			return 0
		}
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, config.RedactedToken)
}

// DecodeAck is a test helper to parse FactsAck from a response body.
func DecodeAck(data []byte) (agentapi.FactsAck, error) {
	var ack agentapi.FactsAck
	err := json.Unmarshal(data, &ack)
	return ack, err
}
