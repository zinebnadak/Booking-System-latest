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
	Meal    int // 3 meals: which meal it is
}

func (s *Server) readyFood(now time.Time) []readyView {
	var out []readyView
	for _, t := range s.Store.ReadyToServe(now) {
		v := readyView{ID: t.ID, Table: t.Table, Meal: t.Meal, At: clockOf(*t.DoneAt), Minutes: minutesSince(*t.DoneAt, now)}
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

// dealWaitView is a 3-meals table between meals, on the kitchen screen.
type dealWaitView struct {
	Table   int
	Guests  int
	Next    int
	Left    []dealDish
	Since   string // when the chef finished the last meal
	Minutes int
}

type dealDish struct {
	No   int
	Name string
}

func (s *Server) dealsWaiting(now time.Time) []dealWaitView {
	var out []dealWaitView
	for _, w := range s.Store.DealsWaiting() {
		v := dealWaitView{Table: w.Table, Guests: w.Guests, Next: w.Next, Since: clockOf(w.Since), Minutes: minutesSince(w.Since, now)}
		for i, d := range w.Left {
			v.Left = append(v.Left, dealDish{No: w.Next + i, Name: d})
		}
		out = append(out, v)
	}
	return out
}
