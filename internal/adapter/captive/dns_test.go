package captive

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"
)

type policy bool

func (p policy) IsCaptive(string) bool { return bool(p) }

func query(name string, qtype uint16) []byte {
	m := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, l := range splitLabels(name) {
		m = append(m, byte(len(l)))
		m = append(m, l...)
	}
	m = append(m, 0, byte(qtype>>8), byte(qtype), 0, 1)
	return m
}

func splitLabels(n string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(n); i++ {
		if i == len(n) || n[i] == '.' {
			out = append(out, n[start:i])
			start = i + 1
		}
	}
	return out
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.LocalAddr().String()
	_ = l.Close()
	return a
}

func startServer(t *testing.T, captive bool, upstream string) string {
	t.Helper()
	addr := freeAddr(t)
	s := &DNSServer{Addr: addr, PortalIP: netip.MustParseAddr("192.168.1.8"), Upstreams: []string{upstream}, Policy: policy(captive)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = s.ListenAndServe(ctx) }()
	time.Sleep(50 * time.Millisecond)
	return addr
}

func exchange(t *testing.T, network, addr string, q []byte) []byte {
	t.Helper()
	c, err := net.DialTimeout(network, addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(6 * time.Second))
	if network == "tcp" {
		if err := writeTCPMsg(c, q); err != nil {
			t.Fatal(err)
		}
		r, err := readTCPMsg(c)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if _, err := c.Write(q); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	return buf[:n]
}

func TestCaptiveClientGetsPortalAddress(t *testing.T) {
	addr := startServer(t, true, "127.0.0.1:1")
	for _, network := range []string{"udp", "tcp"} {
		r := exchange(t, network, addr, query("connectivitycheck.gstatic.com", typeA))
		if binary.BigEndian.Uint16(r[0:2]) != 0x1234 || r[2]&0x80 == 0 || binary.BigEndian.Uint16(r[6:8]) != 1 {
			t.Fatalf("%s: bad header % x", network, r[:12])
		}
		if got := r[len(r)-4:]; !(got[0] == 192 && got[1] == 168 && got[2] == 1 && got[3] == 8) {
			t.Errorf("%s: answer = %v, want 192.168.1.8", network, got)
		}
	}
	r := exchange(t, "udp", addr, query("example.com", 28)) // AAAA
	if binary.BigEndian.Uint16(r[6:8]) != 0 || r[3]&0x0F != 0 {
		t.Errorf("AAAA must be an empty NOERROR answer, got % x", r[:12])
	}
}

func TestNonCaptiveClientIsForwarded(t *testing.T) {
	up, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		buf := make([]byte, 512)
		n, a, err := up.ReadFrom(buf)
		if err != nil {
			return
		}
		resp := append([]byte(nil), buf[:n]...)
		resp[2] |= 0x80
		resp = append(resp, 0xAA) // marker
		_, _ = up.WriteTo(resp, a)
	}()
	addr := startServer(t, false, up.LocalAddr().String())
	r := exchange(t, "udp", addr, query("example.com", typeA))
	if r[len(r)-1] != 0xAA {
		t.Errorf("response did not come from upstream: % x", r)
	}
}

func TestFallsBackToNextUpstream(t *testing.T) {
	up, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		buf := make([]byte, 512)
		n, a, err := up.ReadFrom(buf)
		if err != nil {
			return
		}
		resp := append([]byte(nil), buf[:n]...)
		resp[2] |= 0x80
		_, _ = up.WriteTo(append(resp, 0xBB), a)
	}()
	addr := freeAddr(t)
	s := &DNSServer{Addr: addr, Upstreams: []string{freeAddr(t), up.LocalAddr().String()}, Policy: policy(false)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.ListenAndServe(ctx) }()
	time.Sleep(50 * time.Millisecond)
	r := exchange(t, "udp", addr, query("example.com", typeA))
	if r[3]&0x0F == 2 || r[len(r)-1] != 0xBB {
		t.Fatalf("expected the second upstream's answer, got % x", r)
	}
}

func TestUpstreamDownGivesServfail(t *testing.T) {
	addr := startServer(t, false, freeAddr(t)) // nobody listening
	r := exchange(t, "tcp", addr, query("example.com", typeA))
	if r[3]&0x0F != 2 {
		t.Errorf("rcode = %d, want SERVFAIL", r[3]&0x0F)
	}
}

func TestQuestionRejectsMalformed(t *testing.T) {
	for _, m := range [][]byte{
		nil,
		make([]byte, 11),
		append(query("a.b", typeA)[:12], 0xC0, 0x0C, 0, 1, 0, 1), // compression pointer
		query("a.b", typeA)[:15],                                 // truncated
	} {
		if _, _, err := question(m); err == nil {
			t.Errorf("accepted malformed % x", m)
		}
	}
}

func TestQname(t *testing.T) {
	if got := qname(query("Captive.Apple.com", typeA)); got != "captive.apple.com" {
		t.Errorf("qname = %q", got)
	}
	if got := qname([]byte{1, 2}); got != "" {
		t.Errorf("malformed qname = %q", got)
	}
}
