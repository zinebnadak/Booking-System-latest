package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// Orders shows which tables are booked later today, and the table's own page says so.
func TestOrdersShowBookedTables(t *testing.T) {
	e := newEnv(t, nil)
	ref := refFromLocation(t, e.do("POST", "/book", bookingForm("18:00", "2"), false).Header().Get("Location"))
	e.do("POST", "/dev/pay/"+ref, url.Values{}, false)
	b, _ := e.store.Get(ref)
	if b.TableNo == 0 {
		t.Skip("bookings here aren't given a table")
	}
	want := "Booked 18:00 · Anna Svensson · 2 guests"
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, want)
	e.expect(e.do("GET", fmt.Sprintf("/admin/orders/%d", b.TableNo), nil, true), 200, want)
	// Checked in: it's who is at the table now, not a booking still to come.
	e.do("POST", "/admin/bookings/"+ref+"/arrived", url.Values{}, true)
	if bb, _ := e.store.Get(ref); bb.ArrivedAt == nil {
		t.Fatal("check-in didn't happen")
	}
	if strings.Contains(e.do("GET", "/admin/orders?part=tiles", nil, true).Body.String(), want) {
		t.Fatal("still shown as booked after arriving")
	}
}
