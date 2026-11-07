package db

import (
	"fmt"
	"sort"
	"time"

	"emeal-booking/schedule"
)

// The "3 meals" deal in the kitchen.
//
// Meal 1 goes to the chef when the waiter starts the deal. When it's done,
// the chef's screen keeps a card for the table: "2 meals left: Meat, Rice.
// The guests haven't asked for meal 2 yet." The next meal comes onto the
// screen, with a chime, when the guests press the button on the table (or
// tap "Ready for the next meal" on their phone), or when a waiter sends it.
// If the guests leave early, the waiter ends the deal and the card goes.

// dealTicket is the kitchen ticket for one meal of a deal (nil if there is
// none, or it was cancelled). Callers hold s.mu.
func (s *Store) dealTicket(run, meal int) *Ticket {
	for _, t := range s.d.Tickets {
		if t.Deal == run && t.Meal == meal && t.Live() {
			return t
		}
	}
	return nil
}

// dealDishName is what the chef reads for a meal: the dish, or "Rätt 2"
// when the waiter didn't choose the order.
func dealDishName(r *MealRun, meal int) string {
	if d := r.Dish(meal); d != "" {
		return d
	}
	return fmt.Sprintf("Rätt %d", meal)
}

// sendMeal puts a meal of the deal on the chef's screen, unless it's
// there already, or the table has no open bill. Callers hold s.mu.
func (s *Store) sendMeal(r *MealRun, meal int, now time.Time) bool {
	if meal < 1 || meal > schedule.MealsInDeal || s.dealTicket(r.ID, meal) != nil {
		return false
	}
	b := s.openBillAt(r.Table)
	if b == nil {
		return false
	}
	qty := r.Guests
	if qty < 1 {
		qty = 1
	}
	s.d.NextTicketID++
	s.d.Tickets = append(s.d.Tickets, &Ticket{ID: s.d.NextTicketID, Table: r.Table, BillID: b.ID, At: now,
		Deal: r.ID, Meal: meal, Items: []TicketItem{{Name: dealDishName(r, meal), Qty: qty}}})
	return true
}

// cancelMeal takes back a meal the chef hasn't finished (the button was
// pressed by mistake). The chef sees it crossed out. Callers hold s.mu.
func (s *Store) cancelMeal(run, meal int) {
	t := s.dealTicket(run, meal)
	if t == nil || t.DoneAt != nil {
		return
	}
	for i := range t.Items {
		t.Items[i].Cancelled = true
	}
}

// dealTicketsOut marks a deal's tickets done and served, so they leave the
// kitchen screen: one meal (it's on the table), or all of them (meal 0:
// the deal is over). Callers hold s.mu.
func (s *Store) dealTicketsOut(run, meal int, now time.Time) {
	for _, t := range s.d.Tickets {
		if t.Deal != run || (meal != 0 && t.Meal != meal) || !t.Live() {
			continue
		}
		if t.DoneAt == nil {
			d := now
			t.DoneAt = &d
		}
		if t.ServedAt == nil {
			d := now
			t.ServedAt = &d
		}
	}
}

// dealMealServed: the kitchen (or a waiter) tapped Served on a deal
// ticket, so that meal is on the table now. Callers hold s.mu.
func (s *Store) dealMealServed(t *Ticket, now time.Time) {
	if t.Deal == 0 || t.Meal < 2 {
		return
	}
	for _, r := range s.d.MealRuns {
		if r.ID == t.Deal && r.Ended == nil && r.Meal() == t.Meal-1 {
			if !r.Waiting() {
				r.Ready = append(r.Ready, now)
			}
			r.Served = append(r.Served, now)
			s.dropCall(r.Table)
		}
	}
}

// SendNextMeal: the guests told a waiter they're ready, and the waiter
// sends the next meal to the kitchen.
func (s *Store) SendNextMeal(table int, now time.Time) error {
	return s.changeDeal(func() (bool, error) {
		run := s.activeRun(table)
		if run == nil {
			return false, ErrNoDeal
		}
		if run.LastMeal() {
			return false, ErrLastMeal
		}
		if !run.Waiting() {
			run.Ready = append(run.Ready, now)
		}
		s.dropCall(table)
		if !s.sendMeal(run, run.Meal()+1, now) && s.dealTicket(run.ID, run.Meal()+1) == nil {
			return false, ErrNoBill
		}
		return true, nil
	})
}

// MealSent reports whether the next meal of the deal on a table is with
// the kitchen already.
func (s *Store) MealSent(table int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.activeRun(table)
	return run != nil && s.dealTicket(run.ID, run.Meal()+1) != nil
}

// DealWait is a table on the deal between meals, as the chef sees it.
type DealWait struct {
	Table  int
	Guests int
	Next   int       // the meal the guests haven't asked for yet
	Left   []string  // the meals still to come, in order
	Since  time.Time // when the chef finished the last one
}

// DealsWaiting lists the tables on the deal whose last meal the chef has
// finished, with meals still to come, in table order. A table drops off
// when its next meal is sent, the deal ends, or the bill is closed.
func (s *Store) DealsWaiting() []DealWait {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DealWait
	for _, r := range s.d.MealRuns {
		if r.Ended != nil {
			continue
		}
		b := s.openBillAt(r.Table)
		if b == nil {
			continue
		}
		upTo, cooking := r.Meal(), false
		var since time.Time
		for _, t := range s.d.Tickets {
			if t.Deal != r.ID || !t.Live() {
				continue
			}
			upTo = max(upTo, t.Meal)
			if t.DoneAt == nil {
				cooking = true
			} else if t.DoneAt.After(since) {
				since = *t.DoneAt
			}
		}
		if cooking || upTo >= schedule.MealsInDeal {
			continue
		}
		if since.IsZero() {
			since = r.Since()
		}
		w := DealWait{Table: r.Table, Guests: r.Guests, Next: upTo + 1, Since: since}
		for m := upTo + 1; m <= schedule.MealsInDeal; m++ {
			w.Left = append(w.Left, dealDishName(r, m))
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return schedule.TableOrder(out[i].Table) < schedule.TableOrder(out[j].Table) })
	return out
}
