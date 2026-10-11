package captive

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSNIProxy_AllowedPassesThroughDeniedIsDropped(t *testing.T) {
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "checkout page")
	}))
	defer site.Close()

	addr := freeTCPAddr(t)
	p := &SNIProxy{
		Addr:  addr,
		Allow: []string{"naboopay.com"},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, site.Listener.Addr().String())
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = p.ListenAndServe(ctx) }()
	time.Sleep(50 * time.Millisecond)

	client := func(host string) *http.Client {
		return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
			// The test server's certificate is self-signed; in real use the
			// phone verifies the site's genuine certificate end to end.
			TLSClientConfig: &tls.Config{ServerName: host, InsecureSkipVerify: true}, //nolint:gosec
		}}
	}

	resp, err := client("checkout.naboopay.com").Get("https://checkout.naboopay.com/pay")
	if err != nil {
		t.Fatalf("allowed host: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "checkout page" {
		t.Errorf("body = %q", body)
	}

	if _, err := client("www.youtube.com").Get("https://www.youtube.com/"); err == nil {
		t.Error("a host outside the walled garden must not get through")
	}
}

func TestSNIProxy_Allowed(t *testing.T) {
	p := &SNIProxy{Allow: []string{"naboopay.com", "wave.com"}}
	for host, want := range map[string]bool{
		"naboopay.com": true, "checkout.naboopay.com": true, "PAY.WAVE.COM.": true,
		"evilnaboopay.com": false, "naboopay.com.evil.net": false, "google.com": false, "": false,
	} {
		if got := p.Allowed(host); got != want {
			t.Errorf("Allowed(%q) = %v, want %v", host, got, want)
		}
	}
}

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	_ = l.Close()
	return a
}
