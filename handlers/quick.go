package handlers

import (
	"sort"
	"time"

	"emeal-booking/db"
	"emeal-booking/schedule"
)

// Quicker for waiters on the table page: the dishes and drinks that sell
// most as big buttons on top of the menu, and notes for the chef at a tap.

// favouritesDays: the best sellers are counted over this many days.
const favouritesDays = 30

// favouritesMax: how many big buttons.
const favouritesMax = 8

type favItem struct {
	ID    int
	Name  string
	Drink bool
}

// favourites are the most-sold dishes and drinks of the last 30 days (paid
// bills), that can be ordered right now.
func (s *Server) favourites(now time.Time) []favItem {
	today := schedule.Today(now)
	from := schedule.FormatDate(today.AddDate(0, 0, -favouritesDays))
	qty := map[int]int{}
	for _, b := range s.Store.PaidBillsBetween(from, schedule.FormatDate(today)) {
		for _, l := range b.Lines {
			if l.MenuItemID != 0 {
				qty[l.MenuItemID] += l.Qty
			}
		}
	}
	day := schedule.FormatDate(today)
	var out []favItem
	for _, m := range s.Store.StaffMenu() {
		if qty[m.ID] == 0 || m.Package || m.Category == db.DealCategory || m.Gone(day) || m.PriceMissing() {
			continue
		}
		out = append(out, favItem{ID: m.ID, Name: m.Name, Drink: m.Drink})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if qty[out[i].ID] != qty[out[j].ID] {
			return qty[out[i].ID] > qty[out[j].ID]
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > favouritesMax {
		out = out[:favouritesMax]
	}
	return out
}

// quickNotes are the notes for the chef a waiter can add with one tap
// (translated on the screen; the chef reads them as shown).
var quickNotes = []string{"No onions", "Gluten free", "Lactose free", "No nuts", "Extra sauce", "Sauce on the side", "Well done", "Medium"}
