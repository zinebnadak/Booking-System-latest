package handlers

import (
	"time"

	"ozmo-booking/db"
)

// Food at the pass: when the chef taps Done, the waiters' Orders and Tables
// screens chime "Table 5 · 1× Burger" until someone takes it to the table
// and taps Served (the kitchen can tap it too). The table's tile then says
// "Served · waiting to pay".

// readyFoodLate: after this long at the pass the line turns red.
const readyFoodLate = 4 * time.Minute

// readyView is food waiting at the pass for a waiter.
type readyView struct {
	ID      int
	Table   int
	Items   []db.TicketItem
	At      string // when the chef finished it
	Minutes int    // waiting since then
	Late    bool
}

func (s *Server) readyFood(now time.Time) []readyView {
	var out []readyView
	for _, t := range s.Store.ReadyToServe(now) {
		v := readyView{ID: t.ID, Table: t.Table, At: clockOf(*t.DoneAt), Minutes: minutesSince(*t.DoneAt, now)}
		v.Late = now.Sub(*t.DoneAt) >= readyFoodLate
		for _, it := range t.Items {
			if !it.Cancelled {
				v.Items = append(v.Items, it)
			}
		}
		out = append(out, v)
	}
	return out
}
