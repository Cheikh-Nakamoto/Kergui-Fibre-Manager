package usecase_test

import (
	"context"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase"
)

// TestMACFilterSetup_UnsupportedBox: a box with no MAC filter switch is
// skipped without an error, and the journal says so (#56).
func TestMACFilterSetup_UnsupportedBox(t *testing.T) {
	gw := &fakeGateway{meta: f660Meta()}
	log := &lines{}
	uc := usecase.NewMACFilter(fakeFactory{gw: gw}, newVault(), fakeDisco{}, log)
	in := usecase.MACFilterInput{BaseURL: "http://192.168.1.1", AdapterID: "zte_f660",
		Creds: domain.Credentials{Username: "admin", Password: "x"}}
	res, err := uc.Setup(context.Background(), in)
	if err != nil || res != usecase.SetupUnsupported {
		t.Fatalf("Setup = %q, %v; want unsupported, nil", res, err)
	}
	if log.count("pas d'interrupteur") != 1 {
		t.Errorf("journal = %v, want one line about the missing switch", log.all)
	}
}
