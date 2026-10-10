// Package httpkit provides a small, shared HTTP session helper for router
// gateways: a cookie-jar-backed client, relative-URL resolution, and cookie
// inspection. It deliberately knows nothing about any particular router.
package httpkit

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

const defaultTimeout = 15 * time.Second

// Session is a stateful HTTP conversation with one router. The cookie jar keeps
// the session (SID/CSRF) across requests, exactly as a browser would.
type Session struct {
	base   *url.URL
	client *http.Client
}

// NewSession builds a session for baseURL. InsecureTLS, when set, skips
// certificate verification for THIS router only (home routers ship self-signed
// certs); it never affects anything else.
func NewSession(baseURL string, opts port.RouterOptions) (*Session, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, err
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	// Proxy from environment is kept (ProxyFromEnvironment); LAN/localhost hosts
	// are excluded by NO_PROXY, so router traffic goes direct.
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if opts.InsecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 — router self-signed cert, opt-in
	}
	return &Session{
		base:   u,
		client: &http.Client{Timeout: timeout, Jar: jar, Transport: tr},
	}, nil
}

// BaseURL returns the configured base URL.
func (s *Session) BaseURL() string { return s.base.String() }

func (s *Session) resolve(rel string) (string, error) {
	r, err := url.Parse(rel)
	if err != nil {
		return "", err
	}
	return s.base.ResolveReference(r).String(), nil
}

// Get fetches a relative URL, returning the body and HTTP status.
func (s *Session) Get(ctx context.Context, rel string) (body []byte, status int, err error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, abs, nil)
	if err != nil {
		return nil, 0, err
	}
	return s.do(req)
}

// PostForm submits a urlencoded form to a relative URL.
func (s *Session) PostForm(ctx context.Context, rel string, form url.Values) (body []byte, status int, err error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, abs, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.do(req)
}

// PostRaw submits a pre-encoded body with Content-Type application/x-www-form-urlencoded.
func (s *Session) PostRaw(ctx context.Context, rel string, body string) ([]byte, int, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, abs, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.do(req)
}

// PostRawWithHeaders submits a pre-encoded body with custom headers.
func (s *Session) PostRawWithHeaders(ctx context.Context, rel string, body string, headers map[string]string) ([]byte, int, error) {
	abs, err := s.resolve(rel)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, abs, strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return s.do(req)
}

func (s *Session) do(req *http.Request) ([]byte, int, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // cap at 8 MiB
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return b, resp.StatusCode, nil
}

// Probe performs a single stateless GET for discovery, returning status, headers
// and body. It is non-destructive and never authenticates. It respects the same
// TLS/proxy rules as a Session.
func Probe(ctx context.Context, baseURL, rel string, opts port.RouterOptions) (status int, header http.Header, body []byte, err error) {
	s, err := NewSession(baseURL, opts)
	if err != nil {
		return 0, nil, nil, err
	}
	abs, err := s.resolve(rel)
	if err != nil {
		return 0, nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, abs, nil)
	if err != nil {
		return 0, nil, nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, resp.Header, nil, err
	}
	return resp.StatusCode, resp.Header, b, nil
}

// HasCookie reports whether the jar holds a non-empty cookie with the given name
// for the base URL — the usual signal that a login established a session.
func (s *Session) HasCookie(name string) bool {
	for _, c := range s.client.Jar.Cookies(s.base) {
		if c.Name == name && c.Value != "" {
			return true
		}
	}
	return false
}
