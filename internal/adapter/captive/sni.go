package captive

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// DefaultPaymentHosts is the walled garden: the only HTTPS sites a cut device
// can reach, so it can pay. A name matches itself and its subdomains.
var DefaultPaymentHosts = []string{
	"naboopay.com",
	"wave.com",
	"orange-sonatel.com",
	"orange.sn",
	"sonatel.sn",
}

// SNIProxy relays TLS connections for allowed host names to the real site.
// The captive DNS points every name at kergui; this proxy reads the server
// name the phone asks for (TLS SNI) and, if it is a payment site, splices the
// connection through to it. Encryption stays end to end: kergui never sees the
// content and the site's certificate is the real one.
type SNIProxy struct {
	Addr  string // e.g. 192.168.1.8:443
	Allow []string
	Self  netip.Addr // never dial ourselves (would loop)
	Log   port.Logger
	// Dial reaches the real site; nil means net.Dialer over the system resolver.
	Dial func(ctx context.Context, network, addr string) (net.Conn, error)

	mu      sync.Mutex
	refused map[string]time.Time
}

// ListenAndServe serves until ctx ends.
func (p *SNIProxy) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", p.Addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return ctx.Err()
			}
			continue
		}
		go p.handle(ctx, c)
	}
}

// Allowed reports whether host is in the walled garden.
func (p *SNIProxy) Allowed(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, a := range p.Allow {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" && (host == a || strings.HasSuffix(host, "."+a)) {
			return true
		}
	}
	return false
}

var errHelloRead = errors.New("client hello captured")

func (p *SNIProxy) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	ip := clientIP(c.RemoteAddr())
	if !ip.IsValid() || !isLAN(ip) {
		return
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	host, hello, err := readServerName(c)
	if err != nil || host == "" {
		return
	}
	if !p.Allowed(host) {
		p.noteRefused(host, ip)
		return
	}
	dial := p.Dial
	if dial == nil {
		d := &net.Dialer{Timeout: 10 * time.Second}
		dial = d.DialContext
	}
	up, err := dial(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		if p.Log != nil {
			p.Log.Warnf("walled garden: cannot reach %s for %s: %v", host, ip, err)
		}
		return
	}
	defer up.Close()
	if ra := clientIP(up.RemoteAddr()); ra == p.Self {
		return
	}
	_ = c.SetDeadline(time.Time{})
	if _, err := up.Write(hello); err != nil {
		return
	}
	if p.Log != nil {
		p.Log.Debugf("walled garden: %s -> %s", ip, host)
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); closeWrite(up); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); closeWrite(c); done <- struct{}{} }()
	<-done
	<-done
}

// noteRefused logs a refused name at most once per 10 minutes: the journal
// then lists exactly what a payment page still needs to be allowed.
func (p *SNIProxy) noteRefused(host string, ip netip.Addr) {
	p.mu.Lock()
	if p.refused == nil {
		p.refused = map[string]time.Time{}
	}
	last, seen := p.refused[host]
	now := time.Now()
	if !seen || now.Sub(last) > 10*time.Minute {
		p.refused[host] = now
	}
	p.mu.Unlock()
	if (!seen || now.Sub(last) > 10*time.Minute) && p.Log != nil {
		// Debug: a cut phone's apps knock on dozens of sites; only the few a
		// payment page needs matter, and those show up while testing a payment.
		p.Log.Debugf("walled garden: refused %s (asked by %s); add it to KERGUI_CAPTIVE_ALLOW if a payment page needs it", host, ip)
	}
}

// readServerName reads the TLS ClientHello and returns the requested server
// name plus the raw bytes read, to replay them to the real server. It uses the
// standard TLS stack to parse the hello and stops right after.
func readServerName(c net.Conn) (string, []byte, error) {
	var buf bytes.Buffer
	var name string
	err := tls.Server(helloConn{r: io.TeeReader(c, &buf)}, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			name = h.ServerName
			return nil, errHelloRead
		},
	}).Handshake()
	if name == "" {
		if err == nil {
			err = errors.New("no server name")
		}
		return "", nil, err
	}
	return name, buf.Bytes(), nil
}

// helloConn is a read-only net.Conn: the TLS server parses the hello from it
// and any reply it tries to send is dropped.
type helloConn struct{ r io.Reader }

func (h helloConn) Read(b []byte) (int, error)       { return h.r.Read(b) }
func (h helloConn) Write(b []byte) (int, error)      { return 0, io.ErrClosedPipe }
func (h helloConn) Close() error                     { return nil }
func (h helloConn) LocalAddr() net.Addr              { return nil }
func (h helloConn) RemoteAddr() net.Addr             { return nil }
func (h helloConn) SetDeadline(time.Time) error      { return nil }
func (h helloConn) SetReadDeadline(time.Time) error  { return nil }
func (h helloConn) SetWriteDeadline(time.Time) error { return nil }

func closeWrite(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
		return
	}
	_ = c.Close()
}
