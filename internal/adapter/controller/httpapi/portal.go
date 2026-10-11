package httpapi

import (
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/webui"
)

// portalAssets are the only dashboard files the portal listener may serve.
var portalAssets = map[string]string{
	"/portail/logo.png":    "logo.png",
	"/portail/favicon.ico": "favicon/favicon.ico",
}

// PortalHandler serves the captive portal for devices whose internet is cut.
// It shares nothing with the admin API: every request that is not the portal
// page, its JSON or its two images is redirected to the page, which is what
// makes phones show their "sign in to network" prompt.
func PortalHandler(c *usecase.Captive, portalURL string, log port.Logger) http.Handler {
	mux := http.NewServeMux()
	seen := newOncePer(10 * time.Minute)
	mux.HandleFunc("GET /portail", func(w http.ResponseWriter, _ *http.Request) {
		b, err := fs.ReadFile(webui.FS(), "portal.html")
		if err != nil {
			http.Error(w, "portal unavailable", http.StatusInternalServerError)
			return
		}
		noStore(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /portail/api/me", func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		st, _ := c.Lookup(remoteIP(r))
		// Only what the device's own owner needs: no other device is ever exposed.
		out := map[string]any{"captive": st.Captive, "name": st.Name, "reason": st.Reason}
		if p := st.Payment; p != nil && strings.HasPrefix(p.CheckoutURL, "https://") {
			out["payment"] = map[string]any{
				"amount_xof": p.AmountXOF, "bytes": p.Bytes, "period": p.Period, "checkout_url": p.CheckoutURL,
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /portail/api/later", func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		if err := c.PayLater(r.Context(), remoteIP(r)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	for path, file := range portalAssets {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, webui.FS(), file)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		noStore(w)
		ip := remoteIP(r)
		// One line per device per 10 min: enough to see whether a phone's
		// connectivity check (captive.apple.com, connectivitycheck.gstatic.com…)
		// reaches the portal at all.
		if log != nil && seen.first(ip) {
			st, _ := c.Lookup(ip)
			log.Infof("portal: %s (%s, %s) asked http://%s%s [%s] -> redirected", ip, st.Name, st.Reason,
				r.Host, r.URL.Path, deviceKind(r.UserAgent()))
		}
		http.Redirect(w, r, portalURL, http.StatusFound)
	})
	return mux
}

func deviceKind(ua string) string {
	switch {
	case strings.Contains(ua, "CaptiveNetworkSupport"), strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		return "iOS"
	case strings.Contains(ua, "Android"), strings.Contains(ua, "Dalvik"):
		return "Android"
	case ua == "":
		return "no user-agent"
	default:
		if len(ua) > 40 {
			ua = ua[:40]
		}
		return ua
	}
}

// oncePer answers true the first time a key is seen in each period.
type oncePer struct {
	mu     sync.Mutex
	period time.Duration
	last   map[string]time.Time
}

func newOncePer(p time.Duration) *oncePer { return &oncePer{period: p, last: map[string]time.Time{}} }

func (o *oncePer) first(key string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	if t, ok := o.last[key]; ok && now.Sub(t) < o.period {
		return false
	}
	o.last[key] = now
	return true
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
