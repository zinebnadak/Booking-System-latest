package handlers

import (
	"net/url"
	"strings"
	"testing"
)

func TestOfflinePlan(t *testing.T) {
	e := newEnv(t, nil)
	// Every staff screen carries the "switch to paper" box, hidden until the
	// connection goes, so it can show without the internet.
	for _, p := range []string{"/admin/orders", "/admin/kitchen", "/admin/orders/5", "/admin/day"} {
		e.expect(e.do("GET", p, nil, true), 200, `id="offline-bar"`, "No internet. Switch to paper.")
	}
	// Guests never see it.
	if strings.Contains(e.do("GET", "/takeaway", nil, false).Body.String(), "offline-bar") {
		t.Fatal("guest page has the staff offline box")
	}
	// The printable plan, linked from Orders; waiters can open it too.
	e.expect(e.do("GET", "/admin/orders", nil, true), 200, `href="/admin/offline"`)
	e.do("POST", "/admin/staff", url.Values{"name": {"Lisa"}, "username": {"lisa"}, "password": {"lisa-pass-1"}}, true)
	lisa, _ := e.loginAs(t, "lisa", "lisa-pass-1")
	e.expect(e.as(lisa, "GET", "/admin/offline", nil), 200, "When the internet is down", "Payments: card terminal or cash", "When it&#39;s back")
	e.expect(e.do("GET", "/admin/offline?lang=sv", nil, true), 200, "När internet inte fungerar")
	// The stickers tell guests what to do if the page won't open.
	e.expect(e.do("GET", "/admin/tables/qr", nil, true), 200, "Doesn't open? Ask your waiter")
}
