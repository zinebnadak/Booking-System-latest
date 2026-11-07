package handlers

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"emeal-booking/db"
)

// A guest orders a meal (not just a drink) on his phone, at a table where the
// waitress already ordered for him. The chef gets it with his name, it's on
// the table's bill as his, its ingredients come out of stock, and the
// waiters see when it's ready.
func TestPhoneMealAtAWaiterTable(t *testing.T) {
	e := newEnv(t, nil)
	soda, burger := e.menuItem("Läsk 33 cl"), e.menuItem("E-Meal-burgare")
	beef, _ := e.store.AddIngredient("Burgare 150 g", "st", 3, 0)
	burger.Uses = append(burger.Uses, db.IngredientUse{ID: beef.ID, Amount: 1})
	if _, err := e.store.SaveMenuItem(burger); err != nil {
		t.Fatal(err)
	}

	// The waitress orders a soda for him; then he orders a burger himself.
	location(t, e.do("POST", "/admin/orders/5/add", url.Values{"bill": {"0"}, fmt.Sprintf("qty_%d", soda.ID): {"1"}}, true), "msg=added")
	a := join(t, e, tableCode(t, e, 5), 1)
	a.do("POST", "/g/"+a.key+"/name", url.Values{"name": {"Erik"}})
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{fmt.Sprintf("qty_%d", burger.ID): {"1"}, fmt.Sprintf("note_%d", burger.ID): {"no onions"}}); loc(rec) != "/g/"+a.key+"?msg=ordered" {
		t.Fatalf("phone order: %s", loc(rec))
	}
	b, _ := e.store.OpenBill(5)
	if len(b.Lines) != 2 {
		t.Fatalf("bill: %+v", b.Lines)
	}
	e.expect(e.do("GET", "/admin/kitchen?part=tickets", nil, true), 200, "Table 5", "E-Meal-burgare", "Erik", "no onions")
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "E-Meal-burgare", "Erik", "With the chef", "Still to pay at this table")
	if ing := e.store.Ingredients()[0]; ing.Left != 2 {
		t.Fatalf("burgers left: %d", ing.Left)
	}
	// The chef marks it done: the waiters see it's ready.
	tk := e.store.OpenTickets()
	if len(tk) != 1 {
		t.Fatalf("tickets: %+v", tk)
	}
	later := e.srv.Now().Add(time.Minute)
	e.srv.Now = func() time.Time { return later }
	e.do("POST", fmt.Sprintf("/admin/kitchen/%d/done", tk[0].ID), url.Values{}, true)
	e.expect(e.do("GET", "/admin/orders?part=tiles", nil, true), 200, "Food ready")
	// When the burgers run out, his phone can't order another.
	e.do("POST", fmt.Sprintf("/admin/stock/ingredients/%d", beef.ID), url.Values{"left": {"0"}}, true)
	if rec := a.do("POST", "/g/"+a.key+"/order", url.Values{fmt.Sprintf("qty_%d", burger.ID): {"1"}}); loc(rec) != "/g/"+a.key+"?msg=soldout" {
		t.Fatalf("no burgers left: %s", loc(rec))
	}
}
