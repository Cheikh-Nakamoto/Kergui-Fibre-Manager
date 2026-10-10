// Package mockrouter is an in-process HTTP server that imitates the DOCUMENTED
// ZTE F660 contract, serving the synthetic fixtures. It lets the adapter be
// exercised end-to-end — in unit/integration tests and via `make demo` — without
// a real router. It validates internal consistency against the documented
// protocol, NOT against real hardware.
//
// It is stateful for the access-control list: a block/unblock POST updates an
// in-memory set, and the ACL page is rendered from that set, so a write is
// observable on the next read (and flips a device's Blocked flag).
package mockrouter

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

// Server is a stateful mock ZTE F660.
type Server struct {
	user, pass string
	mu         sync.Mutex
	sessions   map[string]bool
	acl        map[string]bool // canonical MAC -> blocked
}

// New builds a mock that accepts the given credentials (default admin/admin). Its
// ACL is seeded with the two rules from the synthetic fixture.
func New(user, pass string) *Server {
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = "admin"
	}
	return &Server{
		user:     user,
		pass:     pass,
		sessions: map[string]bool{},
		acl: map[string]bool{
			"ac:bb:cc:00:00:33": true, // TV-Salon (matches the devices fixture)
			"ac:bb:cc:00:00:99": true, // a rule for a device not currently present
		},
	}
}

// Handler returns the HTTP handler implementing the contract.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.root)
	mux.HandleFunc("/getpage.gch", s.page)
	mux.HandleFunc("/setpage.gch", s.setpage)
	return mux
}

func (s *Server) root(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.login(w, r)
		return
	}
	w.Header().Set("Server", "mini_httpd")
	_, _ = w.Write([]byte(fixtures.ZTEF660Login))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	okCreds := r.PostForm.Get("Username") == s.user && r.PostForm.Get("Password") == s.pass
	hasToken := r.PostForm.Get("Frm_Logintoken") != ""
	isLogin := r.PostForm.Get("action") == "login"
	if !okCreds || !hasToken || !isLogin {
		// Re-serve the login page without a session cookie (auth failed).
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fixtures.ZTEF660Login))
		return
	}
	sid := newSID()
	s.mu.Lock()
	s.sessions[sid] = true
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "SID", Value: sid, Path: "/"})
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<html><body>OK</body></html>`))
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch r.URL.Query().Get("nextpage") {
	case "status_device_info_t.gch":
		_, _ = w.Write([]byte(fixtures.ZTEF660Status))
	case "net_lan_status_t.gch":
		_, _ = w.Write([]byte(fixtures.ZTEF660Devices))
	case "net_wlan_acl_t.gch":
		_, _ = w.Write([]byte(s.renderACL())) // rendered from live state
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// setpage handles an access-control write (block/unblock).
func (s *Server) setpage(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_ = r.ParseForm()
	mac, err := domain.ParseMAC(r.PostForm.Get("MACAddress"))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	switch r.PostForm.Get("action") {
	case "addMacFilter":
		s.acl[mac.String()] = true
	case "delMacFilter":
		delete(s.acl, mac.String())
	default:
		s.mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<html><body>OK</body></html>`))
}

// renderACL builds the ACL page from the current block set, in the same JS-array
// shape as the fixture.
func (s *Server) renderACL() string {
	s.mu.Lock()
	macs := make([]string, 0, len(s.acl))
	for m, blocked := range s.acl {
		if blocked {
			macs = append(macs, m)
		}
	}
	s.mu.Unlock()
	sort.Strings(macs)

	var b strings.Builder
	b.WriteString("<html><body><script>\n")
	b.WriteString("var _acl_mode = \"black\";\n")
	fmt.Fprintf(&b, "var _acl_num = %d;\n", len(macs))
	for i, m := range macs {
		fmt.Fprintf(&b, "var _acl_%d = new Array(\"%s\",\"block\");\n", i, m)
	}
	b.WriteString("</script></body></html>")
	return b.String()
}

func (s *Server) authed(r *http.Request) bool {
	c, err := r.Cookie("SID")
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[c.Value]
}

func newSID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
