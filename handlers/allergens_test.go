package handlers

import (
	"net/url"
	"reflect"
	"testing"

	"ozmo-booking/db"
)

func TestAllergens(t *testing.T) {
	e := newEnv(t, nil)
	lax := menuID(t, e, "Laxfilé")

	// The manager ticks fish and milk (and something made up, which is dropped).
	e.do("POST", "/admin/menu", url.Values{"id": {lax}, "name": {"Laxfilé"}, "category": {"Huvudrätter"},
		"price": {"26,90"}, "available": {"on"}, "allergen": {"milk", "fish", "glitter"}}, true)
	item, _ := e.store.MenuItem(atoiDefault(lax, 0))
	if !reflect.DeepEqual(item.Allergens, []string{"fish", "milk"}) {
		t.Fatalf("allergens %v", item.Allergens)
	}
	// The Menu page shows them, and the edit window has them ticked.
	e.expect(e.do("GET", "/admin/menu", nil, true), 200, "Contains: fish, milk",
		`name="allergen" value="fish" checked`, "Crustaceans (shrimp, crab, lobster)")
	// Switching the dish off and on keeps them.
	e.do("POST", "/admin/menu/"+lax+"/available", url.Values{}, true)
	e.do("POST", "/admin/menu/"+lax+"/available", url.Values{"available": {"on"}}, true)
	if got, _ := e.store.MenuItem(atoiDefault(lax, 0)); len(got.Allergens) != 2 {
		t.Fatalf("lost allergens: %v", got.Allergens)
	}

	// Guests: on the takeaway page (English and Swedish) and on their phone.
	e.expect(e.do("GET", "/takeaway", nil, false), 200, "<b>Contains:</b> fish, milk", "Allergic to something?")
	e.expect(e.do("GET", "/takeaway?lang=sv", nil, false), 200, "<b>Innehåller:</b> fisk, mjölk", "Allergisk mot något?")
	e.do("POST", "/admin/orders/5/qr", url.Values{"open": {"1"}}, true)
	p := join(t, e, tableCode(t, e, 5), 1)
	e.expect(p.do("GET", "/g/"+p.key, nil), 200, "<b>Contains:</b> fish, milk", "ask your waiter before you order")
	// And the waiter taking an order sees them too.
	e.expect(e.do("GET", "/admin/orders/5", nil, true), 200, "<b>Contains:</b> fish, milk")

	// Untick everything: nothing is claimed.
	e.do("POST", "/admin/menu", url.Values{"id": {lax}, "name": {"Laxfilé"}, "category": {"Huvudrätter"},
		"price": {"26,90"}, "available": {"on"}}, true)
	if got, _ := e.store.MenuItem(atoiDefault(lax, 0)); got.Allergens != nil {
		t.Fatalf("still %v", got.Allergens)
	}
}

func TestEveryAllergenHasWords(t *testing.T) {
	if len(db.Allergens) != 14 {
		t.Fatalf("%d allergens, EU law lists 14", len(db.Allergens))
	}
	for _, a := range db.Allergens {
		w, ok := allergenWords[a]
		if !ok || w.en == "" || w.sv == "" || w.enLong == "" || w.svLong == "" {
			t.Fatalf("%s: missing words %+v", a, w)
		}
	}
}
