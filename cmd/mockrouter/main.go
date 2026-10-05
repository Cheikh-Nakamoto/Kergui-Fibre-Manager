// Command mockrouter runs the bundled mock ZTE F660 so the CLI can be exercised
// end-to-end without real hardware (used by `make demo`). It serves only the
// synthetic fixtures and accepts the credentials passed via flags.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address")
	user := flag.String("user", "admin", "accepted username")
	pass := flag.String("pass", "admin", "accepted password")
	flag.Parse()

	srv := mockrouter.New(*user, *pass)
	log.Printf("mock ZTE F660 (synthetic) listening on http://%s", *addr)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil { //nolint:gosec // local dev mock
		log.Fatal(err)
	}
}
