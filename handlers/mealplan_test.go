package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

// From table 4's page: rice first, then fish, then meat, for 2 guests, saved
// for next time. 2 × 20 € goes on the bill, and when they press for meal 2 the
// waiters see it's the fish.
func TestThreeMealsInTheGuestsOrder(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	e.expect(e.do("GET", "/admin/orders/4", nil, true), 200, "Start 3 meals", "20,00 € per guest goes on the bill", "Meal 1", `name="dish3"`)

	location(t, e.do("POST", "/admin/tables/4/start", url.Values{"back": {"table"}, "guests": {"2"},
		"dish1": {"Ris"}, "dish2": {"Fisk"}, "dish3": {"Kött"}, "save": {"1"}, "plan_name": {"Ris, fisk, kött"}}, true), "/admin/orders/4?msg=dealstarted")
	b, _ := e.store.OpenBill(4)
	if b.Total() != 4000 {
		t.Fatalf("bill %d", b.Total())
	}
	e.expect(e.do("GET", "/admin/orders/4", nil, true), 200, "Ris", "Fisk", "Kött", "2 guests", "Meal 2 served · Fisk")

	e.srv.Now = func() time.Time { return saturday.Add(20 * time.Minute) }
	e.press(e.token(4), "", "", nil)
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Ready for meal 2 · Fisk")

	// Next table uses the saved order.
	plans := e.store.MealPlans()
	if len(plans) != 1 {
		t.Fatalf("saved: %+v", plans)
	}
	e.expect(e.do("GET", "/admin/orders/6", nil, true), 200, "Ris, fisk, kött: Ris → Fisk → Kött")
	location(t, e.do("POST", "/admin/tables/6/start", url.Values{"back": {"table"}, "guests": {"3"}, "plan": {fmt.Sprint(plans[0].ID)}}, true), "/admin/orders/6?msg=dealstarted")
	if run := e.store.TablesNow()[5].Run; run == nil || run.Dish(1) != "Ris" || run.Guests != 3 {
		t.Fatalf("table 6: %+v", run)
	}
	// A meal left out: told what to fix, nothing started.
	location(t, e.do("POST", "/admin/tables/2/start", url.Values{"back": {"table"}, "guests": {"2"}, "dish1": {"Ris"}}, true), "/admin/orders/2?msg=dealbad")
	if _, open := e.store.OpenBill(2); open {
		t.Fatal("charged without starting")
	}
}

// The kitchen's side of 3 meals: meal 1 is on the chef's screen from the
// start. When it's done the chef sees "2 meals left" and that the guests
// haven't asked yet; meal 2 comes up when a waiter sends it. The guests
// leave after two: the waiter stops 3 meals and the kitchen card goes.
func TestThreeMealsInTheKitchen(t *testing.T) {
	e := newEnv(t, nil)
	e.srv.Now = func() time.Time { return saturday }
	location(t, e.do("POST", "/admin/tables/4/start", url.Values{"back": {"table"}, "guests": {"2"},
		"dish1": {"Ris"}, "dish2": {"Fisk"}, "dish3": {"Kött"}}, true), "/admin/orders/4?msg=dealstarted")
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "Table 4", "3 meals · meal 1 of 3", "2×", "Ris")
	tk := e.store.OpenTickets()
	if len(tk) != 1 {
		t.Fatalf("tickets: %+v", tk)
	}
	location(t, e.do("POST", fmt.Sprintf("/admin/kitchen/%d/done", tk[0].ID), url.Values{}, true), "/admin/kitchen")
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "3 meals · between meals", "2 meals left",
		"Fisk", "Kött", "The guests haven&#39;t asked for meal 2 yet")
	e.expect(e.do("GET", "/admin/orders/4", nil, true), 200, "Send meal 2 to the kitchen · Fisk")

	// The guests tell the waitress: she sends meal 2.
	location(t, e.do("POST", "/admin/tables/4/send", url.Values{"back": {"table"}}, true), "/admin/orders/4?msg=mealsent")
	page := e.do("GET", "/admin/kitchen?part=tickets", nil, true).Body.String()
	if strings.Contains(page, "2 meals left") || !strings.Contains(page, "meal 2 of 3") {
		t.Fatalf("meal 2 not on the kitchen screen:\n%s", page)
	}
	e.expect(e.do("GET", "/admin/orders/4", nil, true), 200, "Meal 2 is with the kitchen · Fisk")
	for _, x := range e.store.OpenTickets() {
		e.do("POST", fmt.Sprintf("/admin/kitchen/%d/done", x.ID), url.Values{}, true)
		e.do("POST", fmt.Sprintf("/admin/kitchen/%d/served", x.ID), url.Values{}, true)
	}
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "1 meal left", "Kött")

	// They leave after two: stop 3 meals. Gone from the kitchen.
	location(t, e.do("POST", "/admin/tables/4/end", url.Values{"back": {"table"}}, true), "/admin/orders/4?msg=tablecleared")
	if strings.Contains(e.do("GET", "/admin/kitchen?part=tickets", nil, true).Body.String(), "between meals") {
		t.Fatal("still on the kitchen screen after stopping")
	}
}
