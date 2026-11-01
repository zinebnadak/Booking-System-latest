package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Testing on a phone through phone-test.command: the server thinks it lives
// at localhost, but the phone came through https://….trycloudflare.com. Paying
// the deposit must send the phone back to that address, not to localhost
// ("This site can't be reached").
func TestDepositThroughThePhoneTestLink(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Cfg.BaseURL = "http://localhost:8097"
	req := httptest.NewRequest("POST", "http://abc-def.trycloudflare.com/book", strings.NewReader(bookingForm("18:00", "2").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.RemoteAddr = "10.77.0.1:1234"
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Location"); rec.Code != 303 || !strings.HasPrefix(got, "https://abc-def.trycloudflare.com/dev/pay/") {
		t.Fatalf("deposit page: %d %q", rec.Code, got)
	}

	// The real site keeps its own address whatever the request says.
	e.srv.Cfg.BaseURL = "https://ozmo.ax"
	req = httptest.NewRequest("POST", "http://evil.example/book", strings.NewReader(bookingForm("19:00", "2").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "10.77.0.2:1234"
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Location"); !strings.HasPrefix(got, "https://ozmo.ax/dev/pay/") {
		t.Fatalf("real site: %q", got)
	}
}
