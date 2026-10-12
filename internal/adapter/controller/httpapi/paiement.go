package httpapi

import (
	"io/fs"
	"net/http"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/webui"
)

// paymentReturnAssets are the images of the payment return pages.
var paymentReturnAssets = map[string]string{
	"/paiement/logo.png":    "logo.png",
	"/paiement/favicon.ico": "favicon/favicon.ico",
}

// mountPaymentReturn adds the pages NabooPay sends the payer back to
// (KERGUI_PUBLIC_URL/paiement/merci and /paiement/echec). With the signed
// webhook (webhook.go), they are the only part of kergui published on the
// Internet, through the Cloudflare tunnel (deploy/cloudflared/config.yml
// exposes /paiement/ alone), so they read nothing and show nothing about any
// device or payment.
func mountPaymentReturn(mux *http.ServeMux) {
	page := func(w http.ResponseWriter, _ *http.Request) {
		b, err := fs.ReadFile(webui.FS(), "paiement.html")
		if err != nil {
			http.Error(w, "page unavailable", http.StatusInternalServerError)
			return
		}
		noStore(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	}
	mux.HandleFunc("GET /paiement/merci", page)
	mux.HandleFunc("GET /paiement/echec", page)
	for path, file := range paymentReturnAssets {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, webui.FS(), file)
		})
	}
}
