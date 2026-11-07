package handlers

import (
	"net/url"
	"strings"
	"testing"
)

// The table buttons and ordering with the table's QR code are switched off
// for now (the code stays). The staff screens don't mention them, the
// button API and the old table stickers don't work (a sticker leads to the
// feedback page), and the only QR code to print is the feedback one.
func TestButtonsAndTableQROff(t *testing.T) {
	e := newEnv(t, nil)
	code := tableCode(t, e, 5)
	e.srv.Cfg.TableButtons, e.srv.Cfg.TableQR = false, false

	e.expect(e.do("POST", "/api/press", url.Values{"token": {"x"}}, false), 404)
	e.expect(e.do("GET", "/demo/buttons", nil, false), 404)
	e.expect(e.do("GET", "/owner/buttons", nil, true), 404)
	if loc := loc(e.do("GET", "/t/"+code, nil, false)); loc != "/review" {
		t.Fatalf("old table sticker goes to %q", loc)
	}
	e.expect(e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true), 404)

	orders := e.do("GET", "/admin/orders", nil, true).Body.String()
	for _, gone := range []string{"/owner/buttons", "phone-orders/check", "press the button", "Table QR codes"} {
		if strings.Contains(orders, gone) {
			t.Errorf("Orders still shows %q", gone)
		}
	}
	if !strings.Contains(orders, "Feedback QR code") {
		t.Error("no link to the feedback QR code")
	}
	table := e.do("GET", "/admin/orders/5", nil, true).Body.String()
	if strings.Contains(table, `action="/admin/orders/5/qr"`) || strings.Contains(table, "QR orders") {
		t.Error("the table page still has the QR orders switch")
	}
	stickers := e.do("GET", "/admin/tables/qr", nil, true)
	e.expect(stickers, 200, "Feedback QR code", "review-sticker", "/review")
	if strings.Contains(stickers.Body.String(), "/admin/tables/qr/1/new") {
		t.Error("table stickers still offered")
	}
	e.expect(e.do("GET", "/review", nil, false), 200)
}
