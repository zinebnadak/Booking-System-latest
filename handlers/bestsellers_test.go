package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ozmo-booking/db"
)

// The head manager sees the best dish and the best drink of the month,
// from what the tables paid for. The 3 meals price isn't a dish.
func TestBestDishAndDrink(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Cfg.OwnerUser, e.srv.Cfg.OwnerPassword = "owner", "boss"
	e.h = e.srv.Routes()
	burger, soda := e.menuItem("Ozmo-burgare"), e.menuItem("Läsk 33 cl")
	if !soda.Drink || burger.Drink {
		t.Fatal("menu: soda should be a drink, the burger not")
	}
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"},
		fmt.Sprintf("qty_%d", burger.ID): {"2"}, fmt.Sprintf("qty_%d", soda.ID): {"3"}}, true), "msg=added")
	if _, err := e.store.StartDealWith(6, nil, 4, "", 0, e.srv.Now()); err != nil {
		t.Fatal(err)
	}
	for _, no := range []int{5, 6} {
		b, _ := e.store.OpenBill(no)
		if _, err := e.store.PayBill(b.ID, db.PaidCard, e.srv.Now()); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest("GET", "http://example.test/owner/charts?month="+e.srv.Now().Format("2006-01"), nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: e.srv.makeSession(roleOwner, e.srv.Now())})
	req.Header.Set("Accept-Language", "en")
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	page := rec.Body.String()
	for _, want := range []string{"Best dish", "Ozmo-burgare", "2 sold", "Best drink", "Läsk 33 cl", "3 sold", "Best-selling drinks"} {
		if !strings.Contains(page, want) {
			t.Fatalf("charts page missing %q", want)
		}
	}
	if strings.Contains(page, "3 rätter</span>") {
		t.Fatal("the deal price is listed as a dish")
	}
}
