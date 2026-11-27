package handlers

import (
	"net/url"
	"strings"
	"testing"
)

// The kitchen's "Sold out?" list follows the menu: a new dish shows up, a
// dish switched off or deleted goes, and each change wakes open screens.
// Price lines like "3 rätter" are never on it.
func TestKitchenSoldOutFollowsTheMenu(t *testing.T) {
	e := newEnv(t, nil)
	soldOut := func() string {
		body := e.do("GET", "/admin/kitchen?part=tickets", nil, true).Body.String()
		i := strings.Index(body, `id="k-soldout"`)
		if i < 0 {
			return ""
		}
		return body[i:]
	}
	woke := func(step string, before uint64) {
		t.Helper()
		if e.srv.live.version() == before {
			t.Errorf("%s: open screens weren't told", step)
		}
	}

	v := e.srv.live.version()
	e.do("POST", "/admin/menu", url.Values{"id": {"0"}, "category": {"Desserter"}, "name": {"Åländsk pannkaka"}, "price": {"9,50"}, "available": {"on"}, "sort": {"500"}}, true)
	woke("new dish", v)
	if !strings.Contains(soldOut(), "Åländsk pannkaka") {
		t.Fatal("a new dish isn't on the sold-out list")
	}

	dish := e.menuItem("Åländsk pannkaka")
	v = e.srv.live.version()
	e.do("POST", "/admin/menu/"+itoa(dish.ID)+"/available", url.Values{}, true) // switched off
	woke("switched off", v)
	if strings.Contains(soldOut(), "Åländsk pannkaka") {
		t.Fatal("a dish switched off is still on the sold-out list")
	}

	v = e.srv.live.version()
	e.do("POST", "/admin/menu/"+itoa(dish.ID)+"/delete", url.Values{}, true)
	woke("deleted", v)
	if strings.Contains(soldOut(), "Åländsk pannkaka") {
		t.Fatal("a deleted dish is still on the sold-out list")
	}

	if _, err := e.store.DealItem(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(soldOut(), "3 rätter") {
		t.Fatal("the 3 rätter price line is on the sold-out list")
	}
}
