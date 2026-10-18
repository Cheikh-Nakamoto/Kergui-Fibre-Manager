package httpkit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

func TestPinnedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	pin := CertFingerprint(srv.Certificate().Raw)

	for _, tc := range []struct {
		name    string
		pin     string
		wantErr bool
	}{
		{"no pin accepts any self-signed cert", "", false},
		{"matching pin", pin, false},
		{"matching pin with colons and upper case", upperColons(pin), false},
		{"other pin", "00000000" + pin[8:], true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := NewSession(srv.URL, port.RouterOptions{InsecureTLS: true, PinSHA256: tc.pin})
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = s.Get(context.Background(), "/")
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, ErrPinMismatch) {
				t.Fatalf("err = %v, want ErrPinMismatch", err)
			}
		})
	}
}

func upperColons(hexs string) string {
	out := make([]byte, 0, len(hexs)*3/2)
	for i := 0; i < len(hexs); i += 2 {
		if i > 0 {
			out = append(out, ':')
		}
		for _, c := range []byte(hexs[i : i+2]) {
			if c >= 'a' && c <= 'f' {
				c -= 'a' - 'A'
			}
			out = append(out, c)
		}
	}
	return string(out)
}
