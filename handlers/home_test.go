package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// Orders is the waiters' home page: /admin, the name at the top and the
// first menu link all go there, and after adding to a bill there's a big
// "Back to orders" button. The manager (the boss) starts on the money page,
// with Orders first under More.
func TestOrdersIsHome(t *testing.T) {
	wrongPasswordDelay = 0
	e := newEnv(t, nil)
	e.store.AddStaff("Lisa", "lisa", "a-long-password-1", e.srv.Now())
	waiter, _ := e.loginAs(t, "lisa", "a-long-password-1")
	location(t, e.as(waiter, "GET", "/admin", nil), "/admin/orders")
	if !strings.Contains(e.as(waiter, "GET", "/admin/orders", nil).Body.String(), `class="admin-brand" href="/admin/orders"`) {
		t.Fatal("the name at the top doesn't go to Orders")
	}
	location(t, e.do("GET", "/admin", nil, true), "/owner")
	page := e.do("GET", "/admin/orders", nil, true).Body.String()
	if !strings.Contains(page, `class="admin-brand" href="/owner"`) {
		t.Fatal("the manager's name at the top doesn't go to the money page")
	}
	if o, k := strings.Index(page, `href="/admin/orders"`), strings.Index(page, `href="/admin/kitchen"`); o < 0 || k < 0 || o > k {
		t.Fatal("Orders isn't the first link under More")
	}
	// Tables is part of Orders now: no tab of its own, and old links land on Orders.
	if strings.Contains(page, `href="/admin/tables"`) {
		t.Fatal("still a Tables tab")
	}
	location(t, e.do("GET", "/admin/tables", nil, true), "/admin/orders")

	// A table page always has the way back; after adding, a big one too.
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, `class="btn btn-secondary btn-sm back-home" href="/admin/orders"`)
	soda := e.menuItem("Läsk 33 cl")
	rec := e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", soda.ID): {"1"}}, true)
	page = e.do("GET", loc(rec), nil, true).Body.String()
	if !strings.Contains(page, `class="added-bar"`) || !strings.Contains(page, "Added to the bill.") || strings.Count(page, "Back to orders") < 2 {
		t.Fatalf("no big way back after adding:\n%s", page)
	}
	if strings.Contains(page, `class="toast"`) {
		t.Fatal("the message is shown twice")
	}
	// Opening the table again later: no "Added" bar.
	if strings.Contains(e.do("GET", "/admin/orders/5", nil, true).Body.String(), `class="added-bar"`) {
		t.Fatal("added bar without adding")
	}
}
