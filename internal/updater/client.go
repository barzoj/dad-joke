package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// HTTPReleaseClient talks to a GitHub Releases-compatible API.
type HTTPReleaseClient struct {
	HTTPClient *http.Client
	UserAgent  string // prefix; full UA is "DadJoke/<version>" unless overridden entirely
}

// NewHTTPReleaseClient returns a client with strict timeouts and redirect policy.
func NewHTTPReleaseClient() *HTTPReleaseClient {
	return &HTTPReleaseClient{
		HTTPClient: &http.Client{
			Timeout:       DefaultHTTPTimeout,
			CheckRedirect: CheckRedirect,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 15 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
				MaxIdleConns:          4,
				IdleConnTimeout:       30 * time.Second,
				DisableCompression:    false,
			},
		},
	}
}

// CheckRedirect re-validates every redirect target against ValidateDownloadURL
// and caps the hop count at MaxHTTPRedirects.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxHTTPRedirects {
		return fmt.Errorf("stopped after %d redirects", MaxHTTPRedirects)
	}
	if req.URL == nil {
		return fmt.Errorf("redirect missing url")
	}
	if err := ValidateDownloadURL(req.URL.String()); err != nil {
		return fmt.Errorf("redirect blocked: %w", err)
	}
	// Preserve agent identity headers across redirects.
	if len(via) > 0 {
		if ua := via[0].Header.Get("User-Agent"); ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		if v := via[0].Header.Get("X-Agent-Version"); v != "" {
			req.Header.Set("X-Agent-Version", v)
		}
	}
	return nil
}

type ghReleaseJSON struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// FetchLatest GETs apiURL and parses tag_name + assets. agentVersion is sent
// in both User-Agent and X-Agent-Version.
func (c *HTTPReleaseClient) FetchLatest(ctx context.Context, apiURL, agentVersion string) (ReleaseMeta, error) {
	if err := ValidateDownloadURL(apiURL); err != nil {
		return ReleaseMeta{}, fmt.Errorf("release api url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return ReleaseMeta{}, err
	}
	setAgentHeaders(req, agentVersion)

	resp, err := c.client().Do(req)
	if err != nil {
		return ReleaseMeta{}, fmt.Errorf("fetch latest release: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readBounded(resp.Body, MaxReleaseJSONBytes)
	if err != nil {
		return ReleaseMeta{}, fmt.Errorf("read release json: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return ReleaseMeta{}, fmt.Errorf("fetch latest release: HTTP %d", resp.StatusCode)
	}

	var raw ghReleaseJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return ReleaseMeta{}, fmt.Errorf("parse release json: %w", err)
	}
	if strings.TrimSpace(raw.TagName) == "" {
		return ReleaseMeta{}, fmt.Errorf("parse release json: missing tag_name")
	}
	meta := ReleaseMeta{TagName: raw.TagName}
	for _, a := range raw.Assets {
		meta.Assets = append(meta.Assets, AssetMeta{
			Name:               a.Name,
			BrowserDownloadURL: a.BrowserDownloadURL,
		})
	}
	return meta, nil
}

// Download GETs downloadURL with agent headers and a hard size cap.
func (c *HTTPReleaseClient) Download(ctx context.Context, downloadURL, agentVersion string, maxBytes int64) ([]byte, error) {
	if err := ValidateDownloadURL(downloadURL); err != nil {
		return nil, fmt.Errorf("asset url: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = MaxAssetBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	setAgentHeaders(req, agentVersion)

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("download asset: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := readBounded(resp.Body, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("download asset body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download asset: HTTP %d", resp.StatusCode)
	}
	return body, nil
}

func (c *HTTPReleaseClient) client() *http.Client {
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient
	}
	return NewHTTPReleaseClient().HTTPClient
}

func setAgentHeaders(req *http.Request, agentVersion string) {
	v := NormalizeVersion(agentVersion)
	if v == "" {
		v = "unknown"
	}
	req.Header.Set("User-Agent", "DadJoke/"+v)
	req.Header.Set("X-Agent-Version", v)
	req.Header.Set("Accept", "application/json")
}

func readBounded(r io.Reader, max int64) ([]byte, error) {
	limited := io.LimitReader(r, max+1)
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("response exceeds %d byte limit", max)
	}
	return b, nil
}

// FindAsset returns the asset with the given name, or an error if missing.
func FindAsset(meta ReleaseMeta, name string) (AssetMeta, error) {
	for _, a := range meta.Assets {
		if a.Name == name {
			if strings.TrimSpace(a.BrowserDownloadURL) == "" {
				return AssetMeta{}, fmt.Errorf("asset %q missing browser_download_url", name)
			}
			return a, nil
		}
	}
	return AssetMeta{}, fmt.Errorf("asset %q not found in release %s", name, meta.TagName)
}
