package ztef6600p

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/domain"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/fixtures"
	"github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager/internal/usecase/port"
)

// preemptPage is the relevant part of the page the real box served on
// 2026-11-15 while 192.168.1.2 (user) still held a session.
const preemptPage = `<div id="Level">
<p class="row"><input type="radio" id="radio1" name="preempt_sessid" value=stale0123abcd checked/>&nbsp;&nbsp;
<label for="radio1">&#49;&#57;&#50;&#46;&#49;&#54;&#56;&#46;&#49;&#46;&#50;(&#117;&#115;&#101;&#114;)</label>
</p></div><input type="hidden" name="action" id="action" value="preempt"/>`

func TestGateway_LoginPreemptsParkedSession(t *testing.T) {
	var mu sync.Mutex
	live := "stale0123abcd" // a session left open by an earlier login
	preempts := 0
	cookie := func(r *http.Request) string {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			return ""
		}
		return c.Value
	}
	timeout := `<?xml version="1.0"?><ajax_response_xml_root><IF_ERRORSTR>SessionTimeout</IF_ERRORSTR></ajax_response_xml_root>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		q := r.URL.Query()
		typ, tag := q.Get("_type"), q.Get("_tag")
		switch {
		case typ == "loginData" && tag == "login_entry" && r.Method == http.MethodGet:
			w.Write([]byte(`{"sess_token":"t","lockingTime":0}`))
		case typ == "loginData" && tag == "login_token":
			w.Write([]byte(`<ajax_response_xml_root>12345678</ajax_response_xml_root>`))
		case typ == "loginData" && tag == "login_entry":
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "fresh", Path: "/"})
			w.Write([]byte(`{"sess_token":"t","login_need_refresh":true}`))
		case typ == "loginData" && tag == "login_preempt":
			_ = r.ParseForm()
			if r.PostForm.Get("action") == "preempt" && r.PostForm.Get("preempt_sessid") == live {
				live = cookie(r)
				preempts++
			}
			w.Write([]byte(`{"login_need_refresh":true}`))
		case cookie(r) != live && typ == "menuView":
			http.NotFound(w, r)
		case cookie(r) != live && typ == "menuData":
			w.Write([]byte(timeout))
		case cookie(r) != live:
			w.Write([]byte(preemptPage))
		case typ == "menuView":
			w.Write([]byte(`<script>ready</script>`))
		case typ == "menuData" && tag == "devmgr_statusmgr_lua.lua":
			w.Write([]byte(fixtures.ZTEF6600PStatus))
		default:
			w.Write([]byte(mockMainPage))
		}
	}))
	defer srv.Close()

	gw, err := New(srv.URL, port.RouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := gw.Login(ctx, domain.Credentials{Username: "user", Password: "secret"}); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if preempts != 1 {
		t.Fatalf("preempts = %d, want 1", preempts)
	}
	if gw.rsaPubKey == nil {
		t.Error("RSA key not read from the main page served after preempt")
	}
	info, err := gw.RouterInfo(ctx)
	if err != nil {
		t.Fatalf("RouterInfo after preempt: %v", err)
	}
	if info.Model != "F6600P" {
		t.Errorf("Model = %q, want F6600P", info.Model)
	}
}
