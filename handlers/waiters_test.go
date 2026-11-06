package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"ozmo-booking/db"
)

// Lisa opens table 5: it's hers. Erik adds a drink there but it stays
// Lisa's. Erik's Orders screen shows his own tables (and free ones) unless
// he picks "All tables", where every table says whose it is. He can take a
// table over, and the manager can hand it to anyone.
func TestWaitersTables(t *testing.T) {
	e := newEnv(t, nil)
	soda := e.menuItem("Läsk 33 cl")
	waiter := func(name, user string) *http.Cookie {
		m, err := e.store.AddStaff(name, user, "a-long-password-1", e.srv.Now())
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: sessionCookie, Value: e.srv.makeStaffSession(m, e.srv.Now())}
	}
	lisa, erik := waiter("Lisa", "lisa"), waiter("Erik", "erik")
	add := func(c *http.Cookie, table int) {
		rec := e.as(c, "POST", fmt.Sprintf("/admin/orders/%d/add", table), url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", soda.ID): {"1"}})
		if !strings.Contains(loc(rec), "msg=added") {
			t.Fatalf("add: %d %s", rec.Code, loc(rec))
		}
	}
	add(lisa, 5)
	add(erik, 5) // helping out: still Lisa's
	add(erik, 2)
	b5, _ := e.store.OpenBill(5)
	if b5.WaiterName != "Lisa" {
		t.Fatalf("table 5 waiter %q", b5.WaiterName)
	}

	mine := e.as(erik, "GET", "/admin/orders?part=tiles&view=mine", nil).Body.String()
	if !strings.Contains(mine, "My tables (1)") || !strings.Contains(mine, "1 table is with another waiter") ||
		strings.Contains(mine, `href="/admin/orders/5"`) || !strings.Contains(mine, `href="/admin/orders/2"`) {
		t.Fatalf("Erik's tables:\n%s", mine)
	}
	e.expect(e.as(erik, "GET", "/admin/orders?part=tiles&view=all", nil), 200, `href="/admin/orders/5"`, `class="ot-waiter">Lisa<`, `class="ot-waiter">Erik<`)
	// All tables is where everyone starts.
	e.expect(e.as(lisa, "GET", "/admin/orders?part=tiles", nil), 200, `href="/admin/orders/2"`, `aria-current="page">All tables`)
	// The choice is remembered.
	rec := e.as(erik, "GET", "/admin/orders?view=all", nil)
	if !strings.Contains(rec.Header().Get("Set-Cookie"), "orders_view=all") {
		t.Fatal("view not remembered")
	}

	// Erik takes table 5 over; the manager gives it back to nobody.
	e.expect(e.as(erik, "GET", "/admin/orders/5", nil), 200, "Waiter: Lisa", "Take this table")
	location(t, e.as(erik, "POST", "/admin/orders/5/waiter", url.Values{"bill": {fmt.Sprint(b5.ID)}, "staff": {"me"}}), "msg=waiterset")
	if b, _ := e.store.OpenBill(5); b.WaiterName != "Erik" {
		t.Fatalf("taken over: %q", b.WaiterName)
	}
	location(t, e.do("POST", "/admin/orders/5/waiter", url.Values{"bill": {fmt.Sprint(b5.ID)}, "staff": {"0"}}, true), "msg=waiterset")
	if b, _ := e.store.OpenBill(5); b.Waiter != 0 {
		t.Fatal("not handed back")
	}
	// Nobody has table 5 now: it's at the top for everyone to take.
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Needs a waiter", `data-alert-key="nowaiter-`, "Give", `class="ot-waiter">Erik<`)
	e.expect(e.as(lisa, "GET", "/admin/orders?part=tiles", nil), 200, "Needs a waiter", `value="me">Take it`)
	rec = e.as(lisa, "POST", "/admin/orders/5/waiter", url.Values{"bill": {fmt.Sprint(b5.ID)}, "staff": {"me"}, "back": {"orders"}})
	location(t, rec, "/admin/orders?msg=waiterset")
	if strings.Contains(e.as(lisa, "GET", "/admin/orders?part=tiles", nil).Body.String(), "Needs a waiter") {
		t.Fatal("still needs a waiter")
	}

	// Paid: the manager sees who served the most tables.
	for _, no := range []int{5, 2} {
		b, _ := e.store.OpenBill(no)
		if _, err := e.store.PayBill(b.ID, db.PaidCard, e.srv.Now()); err != nil {
			t.Fatal(err)
		}
	}
	e.expect(e.do("GET", "/admin/staff", nil, true), 200, "Who served the most tables", "<strong>Lisa</strong>", "<strong>Erik</strong>", "Today 1 · ")
	if loc(e.as(lisa, "GET", "/admin/staff", nil)) == "" && e.as(lisa, "GET", "/admin/staff", nil).Code == 200 {
		t.Fatal("a waiter can see the staff page")
	}
}

// Drinks are on the table: the tile has "Card · 3,50 €", and one
// tap (and Yes) closes the bill.
func TestPayFromTheTile(t *testing.T) {
	e := newEnv(t, nil)
	soda := e.menuItem("Läsk 33 cl")
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", soda.ID): {"1"}}, true), "msg=added")
	b, _ := e.store.OpenBill(5)
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Card · "+euro(b.Left()), `action="/admin/orders/5/pay"`)
	location(t, e.do("POST", "/admin/orders/5/pay", url.Values{"bill": {fmt.Sprint(b.ID)}, "method": {"card"}}, true), "/admin/orders?msg=billpaid")
	if _, open := e.store.OpenBill(5); open {
		t.Fatal("still open")
	}
}

// Last month's best sellers are big buttons on top of the menu, and each
// dish has quick notes for the chef.
func TestFavouritesAndQuickNotes(t *testing.T) {
	e := newEnv(t, nil)
	burger, soda := e.menuItem("Ozmo-burgare"), e.menuItem("Läsk 33 cl")
	e.expect(e.do("GET", "/admin/orders/3", nil, true), 200, `class="note-chip" data-note="No onions"`)
	if strings.Contains(e.do("GET", "/admin/orders/3", nil, true).Body.String(), "data-fav=") {
		t.Fatal("favourites before anything was sold")
	}
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"},
		fmt.Sprintf("qty_%d", burger.ID): {"2"}, fmt.Sprintf("qty_%d", soda.ID): {"3"}}, true), "msg=added")
	b, _ := e.store.OpenBill(5)
	if _, err := e.store.PayBill(b.ID, db.PaidCard, e.srv.Now()); err != nil {
		t.Fatal(err)
	}
	page := e.do("GET", "/admin/orders/3", nil, true).Body.String()
	s, bu := strings.Index(page, fmt.Sprintf(`data-fav="%d"`, soda.ID)), strings.Index(page, fmt.Sprintf(`data-fav="%d"`, burger.ID))
	if s < 0 || bu < 0 || s > bu {
		t.Fatalf("favourites: soda %d, burger %d (most sold first)", s, bu)
	}
	// A note chosen with the buttons reaches the chef like a typed one.
	location(t, e.do("POST", "/admin/orders/3/add", url.Values{"bill": {"0"}, "hold": {"0"},
		fmt.Sprintf("qty_%d", burger.ID): {"1"}, fmt.Sprintf("note_%d", burger.ID): {"No onions, Well done"}}, true), "msg=added")
	found := false
	for _, tk := range e.store.OpenTickets() {
		for _, it := range tk.Items {
			found = found || (tk.Table == 3 && it.Note == "No onions, Well done")
		}
	}
	if !found {
		t.Fatal("the note didn't reach the chef")
	}
}
