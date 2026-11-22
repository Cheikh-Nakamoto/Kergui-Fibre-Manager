package mobilecore

import (
	"context"
	"net"
	"net/url"
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
}

// SelfIP implements port.SelfLocator.
func (s *selfLocator) SelfIP(_ context.Context, baseURL string) domain.IP {
	s.mu.RLock()
	find := s.find
	s.mu.RUnlock()
	return find(baseURL)
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
