package mobilecore

import (
	"context"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
)

// selfLocator is the port.SelfLocator of the phone: the address it has on
// the box's network. The phone's Wi-Fi MAC cannot be read on recent Android
// (and it is randomised per network anyway), but its IP is what the box
// lists in its DHCP leases, so that is what auto-reject compares.
type selfLocator struct {
	mu   sync.RWMutex
	find func(baseURL string) domain.IP
	// demoURL and demoIP are the demo box and the address « ce téléphone »
	// has on it: the demo phone itself is on 127.0.0.1, which no device of
	// the demo box has.
	demoURL string
	demoIP  domain.IP
	// macs is this phone's MAC on each box, learnt from the IP match
	// (markSelf): Android's private MAC is stable for one Wi-Fi.
	macs map[string]string
}

// SelfIP implements port.SelfLocator.
func (s *selfLocator) SelfIP(_ context.Context, baseURL string) domain.IP {
	s.mu.RLock()
	find, demoURL, demoIP := s.find, s.demoURL, s.demoIP
	s.mu.RUnlock()
	if demoURL != "" && strings.TrimSuffix(baseURL, "/") == demoURL {
		return demoIP
	}
	return find(baseURL)
}

// setDemo makes ip this phone's address on the demo box at url; an empty
// url ends it.
func (s *selfLocator) setDemo(url string, ip domain.IP) {
	s.mu.Lock()
	s.demoURL, s.demoIP = strings.TrimSuffix(url, "/"), ip
	s.mu.Unlock()
}

// learn records mac as this phone's on box.
func (s *selfLocator) learn(box, mac string) {
	s.mu.Lock()
	if s.macs == nil {
		s.macs = map[string]string{}
	}
	s.macs[box] = mac
	s.mu.Unlock()
}

// learnt is this phone's MAC on box, "" when not learnt yet.
func (s *selfLocator) learnt(box string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.macs[box]
}

// Device marks (#69): what the core says a listed device is to the
// household, in the "Mark" field of GET /api/devices. Unmarked devices have
// no "Mark". The app leaves marked devices out of the Devices tab, and the
// Android watch service out of its alerts and counts.
const (
	// MarkSelf is the phone running the app (#86).
	MarkSelf = "Self"
	// MarkOwner is a phone of the household's owner, as the hub lists them
	// (#87). On the owner's own phone, Self wins.
	MarkOwner = "Owner"
)

// selfMAC is the MAC of this phone among the listed devices (MAC → IP), ""
// when it is not there: the device at this phone's address on the box, else
// the MAC learnt from that match. Off the box's Wi-Fi (no address), nothing.
func (s *selfLocator) selfMAC(ctx context.Context, baseURL, box string, listed map[string]domain.IP) string {
	ip := s.SelfIP(ctx, baseURL)
	if ip.IsZero() {
		return ""
	}
	for mac, at := range listed {
		if at == ip {
			s.learn(box, mac)
			return mac
		}
	}
	if mac := s.learnt(box); mac != "" {
		if _, ok := listed[mac]; ok {
			return mac
		}
	}
	return ""
}

func (s *selfLocator) set(find func(string) domain.IP) {
	s.mu.Lock()
	s.find = find
	s.mu.Unlock()
}

// routeIP is the local address the system would use to reach the box. A UDP
// "connection" sends nothing: it only asks the routing table which interface
// and address go there.
func routeIP(baseURL string) domain.IP {
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() == "" {
		return domain.IP{}
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	conn, err := net.Dial("udp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return domain.IP{}
	}
	defer func() { _ = conn.Close() }()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return domain.IP{}
	}
	ip, err := domain.ParseIP(addr.IP.String())
	if err != nil {
		return domain.IP{}
	}
	return ip
}
