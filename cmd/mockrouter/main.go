// Command mockrouter runs a bundled mock ZTE router so the CLI can be
// exercised end-to-end without real hardware (used by `make demo`).
//
//   - -model f660 serves the documented F660 contract on synthetic fixtures;
//   - -model f6600p replays the F6600P protocol captured on the reference box
//     (RFC-005), the one the relay is written against.
//
// With -tls it serves HTTPS behind a throwaway self-signed certificate and
// prints the certificate's SHA-256 fingerprint, which is what a relay pins.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"flag"
	"log"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/mockrouter"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address")
	user := flag.String("user", "admin", "accepted username")
	pass := flag.String("pass", "admin", "accepted password")
	model := flag.String("model", "f660", "router model to imitate: f660 or f6600p")
	useTLS := flag.Bool("tls", false, "serve HTTPS with a throwaway self-signed certificate")
	flag.Parse()

	var handler http.Handler
	switch strings.ToLower(*model) {
	case "f660":
		handler = mockrouter.New(*user, *pass).Handler()
	case "f6600p":
		handler = mockrouter.NewF6600P(*user, *pass).Handler()
	default:
		log.Fatalf("unknown -model %q: want f660 or f6600p", *model)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if !*useTLS {
		log.Printf("mock ZTE %s listening on http://%s", strings.ToUpper(*model), *addr)
		log.Fatal(srv.ListenAndServe())
	}

	cert, fingerprint, err := selfSignedCert()
	if err != nil {
		log.Fatal(err)
	}
	srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	log.Printf("mock ZTE %s listening on https://%s", strings.ToUpper(*model), *addr)
	log.Printf("certificate SHA-256 (what a relay pins): %s", fingerprint)
	log.Fatal(srv.ListenAndServeTLS("", ""))
}

// selfSignedCert mints a certificate for localhost, valid for a day, and
// returns it with the fingerprint a client would pin. The box's own
// certificate is self-signed too, which is why kergui pins instead of
// verifying a chain.
func selfSignedCert() (tls.Certificate, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "kergui-mockrouter"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	sum := sha256.Sum256(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, hex.EncodeToString(sum[:]), nil
}
