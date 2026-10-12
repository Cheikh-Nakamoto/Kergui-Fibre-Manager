// Package captive implements the captive portal's network side: a small DNS
// server that sends cut devices to kergui (so their phone shows the "sign in to
// network" prompt) and forwards everyone else's queries unchanged.
package captive

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// Policy decides which clients are captive.
type Policy interface {
	// Redirect reports whether a query from ip must get the portal address.
	// probe says whether the name is a phone's connectivity check: devices
	// that are only reminded to pay (not cut) are redirected for those names
	// alone, so their phone shows the portal while everything else works.
	Redirect(ip string, probe bool) bool
}

const (
	typeA       = 1
	captiveTTL  = 5 // seconds: answers must expire fast once the device is restored
	maxMsg      = 4096
	fwdTimeout  = 2 * time.Second
	tcpIdleTime = 10 * time.Second
)

// DNSServer answers on one LAN address, UDP and TCP.
type DNSServer struct {
	Addr     string     // e.g. 192.168.1.8:53
	PortalIP netip.Addr // address returned to captive clients
	// Upstreams are tried in order until one answers: the router first, then
	// public resolvers, because the router's DNS drops bursts of queries.
	Upstreams []string
	Policy    Policy
	Log       port.Logger

	probeSeen oncePer
}

// ListenAndServe serves until ctx ends. It fails fast if the ports cannot be
// bound (53 needs root or CAP_NET_BIND_SERVICE).
func (s *DNSServer) ListenAndServe(ctx context.Context) error {
	pc, err := net.ListenPacket("udp", s.Addr)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", s.Addr)
	if err != nil {
		_ = pc.Close()
		return err
	}
	go func() {
		<-ctx.Done()
		_ = pc.Close()
		_ = ln.Close()
	}()
	go s.serveTCP(ln)
	s.serveUDP(pc)
	return ctx.Err()
}

func (s *DNSServer) serveUDP(pc net.PacketConn) {
	for {
		buf := make([]byte, maxMsg)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go func(msg []byte, addr net.Addr) {
			ip := clientIP(addr)
			if !ip.IsValid() || !isLAN(ip) {
				return
			}
			if resp := s.handle(msg, ip, "udp"); resp != nil {
				_, _ = pc.WriteTo(resp, addr)
			}
		}(buf[:n], addr)
	}
}

func (s *DNSServer) serveTCP(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go func() {
			defer c.Close()
			ip := clientIP(c.RemoteAddr())
			if !ip.IsValid() || !isLAN(ip) {
				return
			}
			for {
				_ = c.SetDeadline(time.Now().Add(tcpIdleTime))
				msg, err := readTCPMsg(c)
				if err != nil {
					return
				}
				resp := s.handle(msg, ip, "tcp")
				if resp == nil || writeTCPMsg(c, resp) != nil {
					return
				}
			}
		}()
	}
}

// probeHosts are the names phones resolve to check for a captive portal.
var probeHosts = map[string]bool{
	"captive.apple.com": true, "www.apple.com": true, "www.appleiphonecell.com": true,
	"connectivitycheck.gstatic.com": true, "connectivitycheck.android.com": true, "clients3.google.com": true,
	"www.google.cn":        true, // Transsion (TECNO, Infinix, itel)
	"connect.rom.miui.com": true, "connectivitycheck.platform.hicloud.com": true,
	"www.msftconnecttest.com": true, "www.msftncsi.com": true, "detectportal.firefox.com": true,
}

// isProbe reports whether name is a connectivity check.
func isProbe(name string) bool {
	if probeHosts[name] {
		return true
	}
	// OPPO / realme / OnePlus (ColorOS): conn-service-us-04.allawnos.com, …
	return strings.HasPrefix(name, "conn-service-") && strings.HasSuffix(name, ".allawnos.com")
}

func (s *DNSServer) handle(msg []byte, client netip.Addr, network string) []byte {
	name := qname(msg)
	probe := isProbe(name)
	if s.Policy.Redirect(client.String(), probe) {
		if probe && s.Log != nil && s.probeSeen.first(client.String()+name) {
			s.Log.Infof("captive dns: %s asked kergui for %s (connectivity check) -> portal %s", client, name, s.PortalIP)
		}
		resp, err := captiveAnswer(msg, s.PortalIP)
		if err != nil {
			return nil
		}
		return resp
	}
	var lastErr error
	for _, up := range s.Upstreams {
		resp, err := forward(msg, network, up)
		if err == nil {
			return resp
		}
		lastErr = err
	}
	if s.Log != nil {
		s.Log.Warnf("captive dns: no upstream answered for %s: %v", client, lastErr)
	}
	return servfail(msg)
}

func forward(msg []byte, network, upstream string) ([]byte, error) {
	c, err := net.DialTimeout(network, upstream, fwdTimeout)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(fwdTimeout))
	if network == "tcp" {
		if err := writeTCPMsg(c, msg); err != nil {
			return nil, err
		}
		return readTCPMsg(c)
	}
	if _, err := c.Write(msg); err != nil {
		return nil, err
	}
	buf := make([]byte, maxMsg)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// captiveAnswer replies to any A query with the portal address, and to every
// other type (AAAA, HTTPS, ...) with an empty answer, so the client falls back
// to IPv4 and lands on the portal.
func captiveAnswer(msg []byte, portal netip.Addr) ([]byte, error) {
	qEnd, qtype, err := question(msg)
	if err != nil {
		return nil, err
	}
	resp := make([]byte, 0, qEnd+16)
	resp = append(resp, msg[:qEnd]...)
	flags := binary.BigEndian.Uint16(msg[2:4])
	// QR=1, keep opcode and RD, set RA, rcode 0.
	binary.BigEndian.PutUint16(resp[2:4], 0x8000|(flags&0x7900)|0x0080)
	binary.BigEndian.PutUint16(resp[4:6], 1) // QDCOUNT
	binary.BigEndian.PutUint16(resp[8:10], 0)
	binary.BigEndian.PutUint16(resp[10:12], 0) // drop EDNS and the rest
	if qtype == typeA && portal.Is4() {
		binary.BigEndian.PutUint16(resp[6:8], 1)
		ip := portal.As4()
		resp = append(resp,
			0xC0, 0x0C, // name: pointer to the question
			0x00, typeA, 0x00, 0x01, // type A, class IN
			0, 0, 0, captiveTTL,
			0x00, 0x04, ip[0], ip[1], ip[2], ip[3])
	} else {
		binary.BigEndian.PutUint16(resp[6:8], 0)
	}
	return resp, nil
}

// servfail turns a query into a SERVFAIL response.
func servfail(msg []byte) []byte {
	qEnd, _, err := question(msg)
	if err != nil {
		return nil
	}
	resp := append([]byte(nil), msg[:qEnd]...)
	flags := binary.BigEndian.Uint16(msg[2:4])
	binary.BigEndian.PutUint16(resp[2:4], 0x8000|(flags&0x7900)|0x0080|2)
	binary.BigEndian.PutUint16(resp[4:6], 1)
	binary.BigEndian.PutUint16(resp[6:8], 0)
	binary.BigEndian.PutUint16(resp[8:10], 0)
	binary.BigEndian.PutUint16(resp[10:12], 0)
	return resp
}

var errMalformed = errors.New("malformed DNS message")

// question validates a standard single-question query and returns the offset
// just past the question and its type.
func question(msg []byte) (end int, qtype uint16, err error) {
	if len(msg) < 12 || msg[2]&0x80 != 0 || binary.BigEndian.Uint16(msg[4:6]) != 1 {
		return 0, 0, errMalformed
	}
	i := 12
	for labels := 0; ; labels++ {
		if i >= len(msg) || labels > 127 {
			return 0, 0, errMalformed
		}
		l := int(msg[i])
		if l == 0 {
			i++
			break
		}
		if l&0xC0 != 0 {
			return 0, 0, errMalformed // no compression in a question
		}
		i += 1 + l
	}
	if i+4 > len(msg) {
		return 0, 0, errMalformed
	}
	return i + 4, binary.BigEndian.Uint16(msg[i : i+2]), nil
}

func readTCPMsg(r io.Reader) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(l[:]))
	if n == 0 || n > 65535 {
		return nil, errMalformed
	}
	msg := make([]byte, n)
	_, err := io.ReadFull(r, msg)
	return msg, err
}

func writeTCPMsg(w io.Writer, msg []byte) error {
	b := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(b, uint16(len(msg)))
	copy(b[2:], msg)
	_, err := w.Write(b)
	return err
}

func clientIP(a net.Addr) netip.Addr {
	ap, err := netip.ParseAddrPort(a.String())
	if err != nil {
		return netip.Addr{}
	}
	return ap.Addr().Unmap()
}

// isLAN refuses anything but private and loopback sources: kergui must never
// become an open resolver.
func isLAN(ip netip.Addr) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

// LANAddr returns the local IPv4 address used to reach host (the router), i.e.
// this machine's address on the LAN.
func LANAddr(host string) (netip.Addr, error) {
	c, err := net.Dial("udp", net.JoinHostPort(host, "53"))
	if err != nil {
		return netip.Addr{}, err
	}
	defer c.Close()
	return clientIP(c.LocalAddr()), nil
}

// qname returns the lower-case name asked in a query, or "".
func qname(msg []byte) string {
	if _, _, err := question(msg); err != nil {
		return ""
	}
	var b []byte
	for i := 12; i < len(msg) && msg[i] != 0; {
		l := int(msg[i])
		if len(b) > 0 {
			b = append(b, '.')
		}
		b = append(b, msg[i+1:i+1+l]...)
		i += 1 + l
	}
	return strings.ToLower(string(b))
}

// oncePer answers true the first time a key is seen in a 10-minute window.
type oncePer struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (o *oncePer) first(key string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.last == nil {
		o.last = map[string]time.Time{}
	}
	now := time.Now()
	if t, ok := o.last[key]; ok && now.Sub(t) < 10*time.Minute {
		return false
	}
	o.last[key] = now
	return true
}
