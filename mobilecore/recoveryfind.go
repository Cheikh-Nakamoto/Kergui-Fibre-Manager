package mobilecore

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/agent"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/trust"
)

// « Récupérer à la maison » finds the relay by itself (#117, audit C4): the
// household does not know the relay's address on the Wi-Fi, and need not.
// The relay is on the same network and answers the relay protocol
// (RFC-005, POST /recovery/v1/challenge on agent.RecoveryPort), so the new
// phone asks the home network's addresses with a short delay each:
//
//   - first the devices the app marked as relays (/api/relay-devices), then
//     the box's other devices, when the core is pointed at a box and
//     logged in;
//   - then the rest of this phone's /24 on the Wi-Fi.
//
// A relay's challenge names its household and carries the bundle it holds,
// signed by the owner. Only a relay of the signed-in household, whose bundle
// is signed by the owner the hub's bundle names and lists that relay, is
// kept; the one holding the latest bundle comes first, since the next bundle
// is signed on top of it. A neighbour's relay, one not paired yet, or a
// bundle signed by someone else are only counted.
//
// Each answer is a challenge the relay hands out (agent.RecoveryMaxNonces at
// once, for agent.RecoveryNonceTTL): the screen searches once, then keeps the
// address for the claims. Nothing secret is sent: the box password goes out
// only with the claim, to the address chosen (RecoverAtHome).

const (
	// findTimeout bounds the whole search.
	findTimeout = 20 * time.Second
	// findDialTimeout is the short delay per address: on the Wi-Fi a relay
	// answers in milliseconds, an empty address never.
	findDialTimeout = 600 * time.Millisecond
	// findProbeTimeout bounds one challenge, connected.
	findProbeTimeout = 3 * time.Second
	// findWorkers is how many addresses are asked at once.
	findWorkers = 32
	// homeGuessURL is where this phone's Wi-Fi address is read when the
	// core is not pointed at a box yet: the usual address of a home box.
	homeGuessURL = "http://192.168.1.1"
)

// foundRelay is one relay of the household on the home network.
type foundRelay struct {
	// Address is what RecoverAtHome takes: the IP (with its port when not
	// agent.RecoveryPort).
	Address string `json:"address"`
	AgentID string `json:"agentId"`
	// Version is the bundle the relay holds.
	Version uint64 `json:"version"`
}

// foundJSON is FindRelaysAtHome's answer.
type foundJSON struct {
	// Relays are the household's, the best first.
	Relays []foundRelay `json:"relays"`
	// Others counts the relays that answered but are not the household's
	// (another household, not paired yet, signed by someone else).
	Others int `json:"others"`
}

type probeOutcome int

const (
	probeNone  probeOutcome = iota // no relay there
	probeOther                     // a relay, not the household's
	probeOurs
)

// FindRelaysAtHome looks for the household's relays on the home Wi-Fi.
// tenantID is the signed-in household; bundle is the hub's GET trust-bundle
// answer ("" when there is none: the household is then the only filter).
// It returns {"relays": [{address, agentId, version}], "others": n}, the
// best relay first; an empty list when none answered.
func (c *Core) FindRelaysAtHome(tenantID, bundle string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", errors.New("mobilecore: finding the relay needs a household")
	}
	var owner ed25519.PublicKey
	if strings.TrimSpace(bundle) != "" {
		var cur signedJSON
		if err := json.Unmarshal([]byte(bundle), &cur); err != nil {
			return "", fmt.Errorf("%w: trust bundle: %v", trust.ErrMalformed, err)
		}
		held, err := selfSigned(cur.Payload, cur.Sig)
		if err != nil {
			return "", err
		}
		if held.TenantID != tenantID {
			return "", fmt.Errorf("%w: the hub's bundle is for another household", trust.ErrTenant)
		}
		owner = held.Owner.SigningKey()
	}
	ctx, cancel := context.WithTimeout(context.Background(), findTimeout)
	defer cancel()
	candidates := c.homeCandidatesFn
	if candidates == nil {
		candidates = c.homeCandidates
	}
	found := c.findRelaysAt(ctx, tenantID, owner, candidates(ctx))
	c.log.Infof("recovery: %d relay(s) of the household found on the Wi-Fi, %d other(s)", len(found.Relays), found.Others)
	out, err := json.Marshal(found)
	return string(out), err
}

// findRelaysAt asks each candidate for a challenge, a few at a time.
func (c *Core) findRelaysAt(ctx context.Context, tenantID string, owner ed25519.PublicKey, candidates []string) foundJSON {
	client := c.recoveryClient
	if client == nil {
		client = &http.Client{
			Timeout: findProbeTimeout,
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: findDialTimeout}).DialContext,
				ResponseHeaderTimeout: findProbeTimeout,
				DisableKeepAlives:     true,
			},
		}
	}
	type result struct {
		relay   foundRelay
		outcome probeOutcome
	}
	results := make([]result, len(candidates))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(findWorkers, len(candidates)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				r, o := probeRelay(ctx, client, candidates[i], tenantID, owner)
				results[i] = result{r, o}
			}
		}()
	}
feed:
	for i := range candidates {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	out := foundJSON{Relays: []foundRelay{}}
	for _, r := range results {
		switch r.outcome {
		case probeOurs:
			out.Relays = append(out.Relays, r.relay)
		case probeOther:
			out.Others++
		}
	}
	// The latest bundle first; on a tie, the order tried (the marked
	// relays first).
	sort.SliceStable(out.Relays, func(a, b int) bool { return out.Relays[a].Version > out.Relays[b].Version })
	return out
}

// probeRelay asks addr for a recovery challenge and says whose relay it is.
func probeRelay(ctx context.Context, client *http.Client, addr, tenantID string, owner ed25519.PublicKey) (foundRelay, probeOutcome) {
	base, err := recoveryURL(addr)
	if err != nil {
		return foundRelay{}, probeNone // never off the home network
	}
	ctx, cancel := context.WithTimeout(ctx, findProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/recovery/v1/challenge", strings.NewReader("{}"))
	if err != nil {
		return foundRelay{}, probeNone
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return foundRelay{}, probeNone
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if err != nil {
		return foundRelay{}, probeNone
	}
	if res.StatusCode != http.StatusOK {
		// A relay's refusal names its reason (agent.recoveryError): a
		// relay, not paired yet or busy. Anything else is no relay.
		var refusal struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(data, &refusal) == nil && refusal.Code != "" {
			return foundRelay{}, probeOther
		}
		return foundRelay{}, probeNone
	}
	var ch agent.RecoveryChallenge
	if err := json.Unmarshal(data, &ch); err != nil || ch.TenantID == "" {
		return foundRelay{}, probeNone
	}
	held, err := selfSigned(ch.Bundle.Payload, ch.Bundle.Sig)
	if err != nil || held.TenantID != ch.TenantID || ch.TenantID != tenantID {
		return foundRelay{}, probeOther
	}
	if owner != nil && !held.Owner.SigningKey().Equal(owner) {
		return foundRelay{}, probeOther
	}
	if _, ok := held.Relay(ch.AgentID); !ok {
		return foundRelay{}, probeOther
	}
	return foundRelay{Address: addr, AgentID: ch.AgentID, Version: held.Version}, probeOurs
}

// homeCandidates are the addresses to ask, in order: the box's devices the
// app marked as relays, the box's other devices, then this phone's /24.
// Never this phone, never twice.
func (c *Core) homeCandidates(ctx context.Context) []string {
	c.mu.RLock()
	routerURL, configured := c.routerURL, c.handler != nil
	c.mu.RUnlock()
	at := routerURL
	if at == "" {
		at = homeGuessURL
	}
	self := c.self.SelfIP(ctx, at).String()
	seen := map[string]bool{"": true, self: true}
	var out []string
	add := func(ip string) {
		if !seen[ip] {
			seen[ip] = true
			out = append(out, ip)
		}
	}
	if configured {
		listed := c.listedIPs()
		for _, mac := range c.relays.list(c.boxID()) {
			add(listed[mac])
		}
		for _, mac := range sortedKeys(listed) {
			add(listed[mac])
		}
	}
	for _, ip := range sweep(self) {
		add(ip)
	}
	return out
}

// listedIPs is the box's device list, MAC → IP, when the core is logged in.
func (c *Core) listedIPs() map[string]string {
	out := map[string]string{}
	res, err := c.Request(http.MethodGet, "/api/devices", "")
	if err != nil || res.Status != http.StatusOK {
		return out
	}
	var list []struct{ MAC, IP string }
	if json.Unmarshal([]byte(res.Body), &list) != nil {
		return out
	}
	for _, d := range list {
		if m, ok := canonicalMAC(d.MAC); ok && d.IP != "" {
			out[m] = d.IP
		}
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sweep is the other hosts of self's /24 when self is a private IPv4: the
// home network. Off it (mobile data, loopback, none), nothing.
func sweep(self string) []string {
	ip, err := netip.ParseAddr(self)
	if err != nil || !ip.Is4() || !ip.IsPrivate() {
		return nil
	}
	b := ip.As4()
	out := make([]string, 0, 253)
	for h := 1; h <= 254; h++ {
		if byte(h) == b[3] {
			continue
		}
		out = append(out, netip.AddrFrom4([4]byte{b[0], b[1], b[2], byte(h)}).String())
	}
	return out
}
