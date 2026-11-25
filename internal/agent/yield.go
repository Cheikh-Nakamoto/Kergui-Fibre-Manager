package agent

import (
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// « Proprio prioritaire » (issue #94). The box keeps one admin session. At
// home the owner's app talks to the box directly, and its session wins: it
// evicts the relay's (the adapter's preempt). The relay then yields instead
// of evicting the owner back:
//
//   - it never preempts a session the box names with another address than
//     its own: its own stale session (each call logs in again) it still
//     evicts, as before;
//   - having yielded, it pauses: it keeps its heartbeat, its lease and its
//     long poll, so the hub still hears it and nobody else takes the box, but
//     it does no box work (snapshots, auto-reject, a member's command) until
//     a backoff has run out; then one login tells whether the box is free;
//   - a command the owner signed that reaches it through the hub means the
//     owner is away (the app goes through the relay only then): it takes the
//     box back at once, evicting what the owner left open.
//
// So the two never fight: the owner at home always gets the box, the owner
// away always gets the relay, and a paused relay is "paused", not "offline".

// ErrOwnerAtHome refuses a command that is not the owner's while the relay
// has yielded the box to the owner's own session.
var ErrOwnerAtHome = errors.New("agent: the owner is using the box at home, the relay is paused")

// DefaultYieldBackoff is how long a relay that yielded leaves the box alone
// before it looks again: a few owner polls, so it does not log in beside an
// owner who is still there every round.
const DefaultYieldBackoff = 2 * time.Minute

// Yield is the relay's box-session policy. The adapter asks it before
// evicting another session (port.RouterOptions.Preempt = AllowPreempt); the
// relay loop reads it to pause. It is built before the handler the relay
// runs on (NewYield) and handed to the agent in Config.Yield, because the
// router options are fixed before the agent exists.
type Yield struct {
	mu      sync.Mutex
	lanIP   string
	now     func() time.Time
	backoff time.Duration
	log     port.Logger

	// until is when the relay looks at the box again; zero when it does
	// not yield.
	until  time.Time
	holder string
	// force counts the owner commands running: they take the box back.
	force int
}

// NewYield returns a policy that yields to nobody until the agent sets it
// up (New does).
func NewYield() *Yield { return &Yield{} }

func (y *Yield) setup(lanIP string, now func() time.Time, backoff time.Duration, log port.Logger) {
	y.mu.Lock()
	defer y.mu.Unlock()
	y.lanIP, y.now, y.backoff, y.log = strings.TrimSpace(lanIP), now, backoff, log
}

// AllowPreempt is port.RouterOptions.Preempt for the relay: true evicts the
// session the box names, false yields to it.
func (y *Yield) AllowPreempt(holder string) bool {
	if y == nil {
		return true
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.force > 0 || y.now == nil {
		return true
	}
	// Without its own address the relay cannot tell its own stale session
	// from someone else's; yielding then would lock it out of the box for
	// good, so it keeps the old behaviour.
	ip := holderIP(holder)
	if y.lanIP == "" || ip == "" || ip == y.lanIP {
		return true
	}
	if !y.pausedLocked() && y.log != nil {
		y.log.Infof("agent: the box is in use by %s (the owner at home), the relay yields and pauses", holder)
	}
	y.until = y.now().Add(y.backoff)
	y.holder = holder
	return false
}

// Paused reports whether the relay leaves the box alone right now, and to
// whom it yielded.
func (y *Yield) Paused() (bool, string) {
	if y == nil {
		return false, ""
	}
	y.mu.Lock()
	defer y.mu.Unlock()
	return y.pausedLocked(), y.holder
}

func (y *Yield) pausedLocked() bool {
	return y.force == 0 && y.now != nil && !y.until.IsZero() && y.now().Before(y.until)
}

// takeBack runs an owner command: the box is taken back whoever holds it,
// and the pause ends. The returned func ends the command.
func (y *Yield) takeBack() (done func()) {
	y.mu.Lock()
	defer y.mu.Unlock()
	if y.pausedLocked() && y.log != nil {
		y.log.Infof("agent: a command from the owner came through the hub, the owner is away: taking the box back")
	}
	y.force++
	y.until, y.holder = time.Time{}, ""
	return func() {
		y.mu.Lock()
		y.force--
		y.mu.Unlock()
	}
}

// holderIP reads the address out of the box's name for a session,
// "192.168.1.17(user)"; "" when it carries none.
func holderIP(holder string) string {
	h := strings.TrimSpace(holder)
	if i := strings.IndexByte(h, '('); i >= 0 {
		h = strings.TrimSpace(h[:i])
	}
	if net.ParseIP(h) == nil {
		return ""
	}
	return h
}
