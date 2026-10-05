// Package mockrouter is an in-process HTTP server that imitates the DOCUMENTED
// ZTE F660 contract, serving the synthetic fixtures. It lets the adapter be
// exercised end-to-end — in unit/integration tests and via `make demo` — without
// a real router. It validates internal consistency against the documented
// protocol, NOT against real hardware.
package mockrouter

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
)

// Server is a stateful mock ZTE F660.
type Server struct {
	user, pass string
	mu         sync.Mutex
	sessions   map[string]bool
}

// New builds a mock that accepts the given credentials (default admin/admin).
func New(user, pass string) *Server {
	if user == "" {
		user = "admin"
	}
	if pass == "" {
		pass = "admin"
	}
	return &Server{user: user, pass: pass, sessions: map[string]bool{}}
}

// Handler returns the HTTP handler implementing the contract.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.root)
	mux.HandleFunc("/getpage.gch", s.page)
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
		_, _ = w.Write([]byte(fixtures.ZTEF660AccessControl))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
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
