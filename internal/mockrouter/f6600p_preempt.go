package mockrouter

import (
	"html"
	"net"
	"net/http"
)

// The box's second-session behaviour (#94), off unless EnableParking turns it
// on. Seen live on 2026-11-15: a login while another session is open is
// accepted but parked. The parked client's GET / answers a page naming the
// open session ("192.168.1.5(user)") with a radio per session, every other
// page SessionTimeout, until it POSTs action=preempt to login_preempt; the
// session it names is then evicted and the parked one goes live.
//
// The default (a login evicts the previous session at once) is what the older
// tests rely on; parking is what lets a test put the owner's phone and a relay
// in front of the same box and watch who yields.

// EnableParking makes a login beside a live session wait for a preempt, as
// the real box does.
func (s *F6600P) EnableParking() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parking = true
	if s.parked == nil {
		s.parked = map[string]string{}
	}
}

// HoldSession opens the admin session for another client the box names
// label, e.g. "192.168.1.17(user)": the owner's phone at home. Whoever was
// live is evicted, as by a preempt.
func (s *F6600P) HoldSession(label string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session = "held-" + newSID()
	s.holder = label
	s.views = map[string]bool{}
	s.lastView = ""
}

// DropSession ends the live session, as the box does once it sits idle: the
// owner's phone left home without logging out.
func (s *F6600P) DropSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session, s.holder = "", ""
}

// Holder is how the box names the live session ("" when none).
func (s *F6600P) Holder() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == "" {
		return ""
	}
	return s.holder
}

// Preempts counts the sessions evicted through login_preempt.
func (s *F6600P) Preempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.preempts
}

// clientLabel is how the box names a client's session: its address and the
// account ("192.168.1.5(user)").
func clientLabel(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return host + "(user)"
}

// park records a login made beside a live session; it reports false when the
// box is free or parking is off, and the login goes live at once.
func (s *F6600P) park(sid, label string) bool {
	if !s.parking || s.session == "" {
		return false
	}
	s.parked[sid] = label
	return true
}

// parkedCookie reports whether the request comes from a parked login.
func (s *F6600P) parkedCookie(r *http.Request) bool {
	c, err := r.Cookie(f6600pCookie)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.parked[c.Value]
	return ok
}

// preemptPage is the page a parked client gets: the live session, named.
func (s *F6600P) preemptPage() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return `<html><body><div id="Level"><p class="row">` +
		`<input type="radio" id="radio1" name="preempt_sessid" value=` + s.session + ` checked/>&nbsp;&nbsp;` +
		`<label for="radio1">` + html.EscapeString(s.holder) + `</label></p></div>` +
		`<input type="hidden" name="action" id="action" value="preempt"/></body></html>`
}

// preempt evicts the session the parked client names, and puts it live.
func (s *F6600P) preempt(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	c, err := r.Cookie(f6600pCookie)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil && r.PostForm.Get("action") == "preempt" && r.PostForm.Get("preempt_sessid") == s.session {
		if label, ok := s.parked[c.Value]; ok {
			delete(s.parked, c.Value)
			s.session, s.holder = c.Value, label
			s.views = map[string]bool{}
			s.lastView = ""
			s.preempts++
		}
	}
	_, _ = w.Write([]byte(`{"login_need_refresh":true}`))
}
