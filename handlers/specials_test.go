package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

func TestTuesdaySpecialWithIngredients(t *testing.T) {
	e := newEnv(t, nil)
	tuesday := time.Date(2026, 11, 3, 12, 0, 0, 0, schedule.Location)
	e.srv.Now = func() time.Time { return tuesday }

	// The kitchen counts spaghetti and cheese on the Stock page.
	location(t, e.do("POST", "/admin/stock/ingredients", url.Values{"name": {"Spaghetti"}, "unit": {"g"}, "left": {"1000"}, "lowat": {"300"}}, true), "/admin/stock")
	location(t, e.do("POST", "/admin/stock/ingredients", url.Values{"name": {"Ost"}, "unit": {"g"}, "left": {"100"}}, true), "/admin/stock")
	location(t, e.do("POST", "/admin/stock/ingredients", url.Values{"name": {"ost"}, "unit": {"g"}, "left": {"5"}}, true), "msg=ingredienttaken")
	location(t, e.do("POST", "/admin/stock/ingredients", url.Values{"name": {"Mjöl"}, "left": {"lots"}}, true), "msg=ingredientbad")
	ings := e.store.Ingredients()
	if len(ings) != 2 {
		t.Fatalf("ingredients: %v", ings)
	}
	cheese, spaghetti := ings[0], ings[1]
	page := e.do("GET", "/admin/stock", nil, true)
	e.expect(page, 200, "Ingredients", "Spaghetti", "1000 g left", "No dish uses it yet")

	// A waiter types in today's special and keeps it on the menu today,
	// using 150 g spaghetti and 40 g cheese a portion.
	e.expect(e.do("GET", "/admin/orders/4", nil, true), 200, "Keep it on the menu?", "Uses from stock (one portion)", "Spaghetti (g)")
	rec := e.do("POST", "/admin/orders/4/add", url.Values{"bill": {"0"},
		"custom_name": {"Pasta med ost"}, "custom_price": {"16,50"}, "custom_qty": {"2"}, "custom_type": {"food"},
		"custom_keep":    {"today"},
		"custom_use_ing": {fmt.Sprint(spaghetti.ID), fmt.Sprint(cheese.ID), ""},
		"custom_use_amt": {"150", "40", ""}}, true)
	location(t, rec, "msg=added")
	var special db.MenuItem
	for _, m := range e.store.StaffMenu() {
		if m.Name == "Pasta med ost" {
			special = m
		}
	}
	if special.ID == 0 || special.Category != db.SpecialsCategory || special.Until != "2026-11-03" || len(special.Uses) != 2 {
		t.Fatalf("special: %+v", special)
	}
	if b, _ := e.store.OpenBill(4); len(b.Lines) != 1 || b.Lines[0].MenuItemID != special.ID || b.Lines[0].Qty != 2 {
		t.Fatalf("bill: %+v", b.Lines)
	}
	// 2 portions: 700 g spaghetti and 20 g cheese left. Cheese for no more.
	sp, ch := e.store.Ingredients()[1], e.store.Ingredients()[0]
	if sp.Left != 700 || ch.Left != 20 {
		t.Fatalf("left: %d g spaghetti, %d g cheese", sp.Left, ch.Left)
	}
	page = e.do("GET", "/admin/orders/5", nil, true)
	e.expect(page, 200, "Dagens special", "Pasta med ost", "No Ost left", "Out of stock:")
	if strings.Contains(page.Body.String(), fmt.Sprintf(`name="qty_%d"`, special.ID)) {
		t.Fatal("can still add the special with no cheese")
	}
	rec = e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", special.ID): {"1"}}, true)
	location(t, rec, "msg=soldout")

	// More cheese arrives: it's back, with how many can be made.
	e.do("POST", fmt.Sprintf("/admin/stock/ingredients/%d", cheese.ID), url.Values{"left": {"100"}, "lowat": {""}}, true)
	page = e.do("GET", "/admin/orders/5", nil, true)
	e.expect(page, 200, fmt.Sprintf(`name="qty_%d"`, special.ID), "2 left") // 100 g cheese at 40 g = 2
	e.expect(e.do("GET", "/admin/stock", nil, true), 200, "Used in: Pasta med ost")
	e.expect(e.do("GET", "/admin/activity?date=2026-11-03", nil, true), 200, "added a special", "added an ingredient", "counted an ingredient")

	// Wednesday: it's off the order form by itself, still on the Menu page as ended.
	e.srv.Now = func() time.Time { return tuesday.AddDate(0, 0, 1) }
	if strings.Contains(e.do("GET", "/admin/orders/5", nil, true).Body.String(), "Pasta med ost") {
		t.Fatal("the special is still on the order form on Wednesday")
	}
	e.expect(e.do("GET", "/admin/menu", nil, true), 200, "Pasta med ost", "Ended")

	// The manager links a menu dish to the stock and gives it a last day.
	lax := e.menuItem("Laxfilé")
	f := url.Values{"id": {fmt.Sprint(lax.ID)}, "name": {lax.Name}, "category": {lax.Category}, "price": {"26,90"},
		"type": {"food"}, "course": {"2"}, "available": {"on"}, "until": {"2026-11-10"},
		"use_ing": {fmt.Sprint(spaghetti.ID)}, "use_amt": {"80"}}
	location(t, e.do("POST", "/admin/menu", f, true), "msg=saved")
	if m, _ := e.store.MenuItem(lax.ID); m.Until != "2026-11-10" || len(m.Uses) != 1 || m.Uses[0].Amount != 80 {
		t.Fatalf("lax: %+v", m)
	}
	// A day that isn't a date; a special without a valid last day.
	f.Set("until", "soon")
	location(t, e.do("POST", "/admin/menu", f, true), "msg=invalid")
	location(t, e.do("POST", "/admin/orders/6/add", url.Values{"bill": {"0"}, "custom_name": {"X"}, "custom_price": {"5"},
		"custom_keep": {"until"}, "custom_until": {"2026-11-01"}}, true), "msg=invalid")

	// Removing an ingredient takes it off the dishes.
	location(t, e.do("POST", fmt.Sprintf("/admin/stock/ingredients/%d", spaghetti.ID), url.Values{"do": {"remove"}}, true), "/admin/stock")
	if m, _ := e.store.MenuItem(lax.ID); len(m.Uses) != 0 {
		t.Fatalf("lax still uses it: %+v", m.Uses)
	}
}
