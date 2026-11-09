package db

import (
	"crypto/subtle"
	"errors"
	"sort"
	"time"

	"emeal-booking/schedule"
)

// The buttons on the tables and the "3 meals" weekend deal.
//
// Every table has a real push button with a secret code inside. When guests
// press it, the button sends the code to the site, and staff see the table
// light up on their Tables screen:
//
//   - during the deal: "Table 4 – ready for meal 2" (or "finished all 3")
//   - any other time:  "Table 4 – wants a waiter"
//
// Staff then press "Meal 2 served", "Clear table" or "Done".

var (
	ErrNoButton    = errors.New("unknown button code")
	ErrDealRunning = errors.New("the deal is already running on this table")
	ErrNoDeal      = errors.New("no deal is running on this table")
	ErrLastMeal    = errors.New("all meals are already served")
	ErrNoTable     = errors.New("no such table")
)

// PressGap: presses closer together than this count as one (a finger that
// bounces, or a guest pressing twice quickly).
const PressGap = 3 * time.Second

// Button is the physical call button on one table.
type Button struct {
	Table    int        `json:"table"`
	Token    string     `json:"token"`               // the secret code saved in the button
	LastSeen *time.Time `json:"last_seen,omitempty"` // last time the button got through
	Battery  int        `json:"battery,omitempty"`   // percent, if the button sends it (0 = unknown)
}

// MealRun is the "3 meals" deal on one table, from the first meal until the
// table is cleared.
type MealRun struct {
	ID      int         `json:"id"`
	Table   int         `json:"table"`
	Date    string      `json:"date"`
	Served  []time.Time `json:"served"`            // when each meal reached the table
	Ready   []time.Time `json:"ready,omitempty"`   // when the guests had finished each meal
	Ended   *time.Time  `json:"ended,omitempty"`   // table cleared (or deal stopped)
	Stopped bool        `json:"stopped,omitempty"` // ended by staff before the last meal was finished
	Dishes  []string    `json:"dishes,omitempty"`  // what each meal is, in the order this table wants them
	Guests  int         `json:"guests,omitempty"`  // how many guests have the deal (put on the bill)
}

// Dish is what meal n (1, 2, 3) is at this table ("" if not chosen).
func (m MealRun) Dish(n int) string {
	if n >= 1 && n <= len(m.Dishes) {
		return m.Dishes[n-1]
	}
	return ""
}

// Meal is the meal on the table now (1, 2 or 3).
func (m MealRun) Meal() int { return len(m.Served) }

// Waiting reports whether the guests have finished the meal on the table.
func (m MealRun) Waiting() bool { return len(m.Ready) >= len(m.Served) }

// LastMeal reports whether the meal on the table is the last one.
func (m MealRun) LastMeal() bool { return len(m.Served) >= schedule.MealsInDeal }

// Since is when the current step began: the meal was served, or the guests
// said they were ready.
func (m MealRun) Since() time.Time {
	if m.Waiting() && len(m.Ready) > 0 {
		return m.Ready[len(m.Ready)-1]
	}
	if len(m.Served) > 0 {
		return m.Served[len(m.Served)-1]
	}
	return time.Time{}
}

func (m *MealRun) clone() MealRun {
	c := *m
	c.Served = append([]time.Time(nil), m.Served...)
	c.Ready = append([]time.Time(nil), m.Ready...)
	return c
}

// Call is a button press that staff haven't answered yet.
type Call struct {
	Table   int       `json:"table"`
	At      time.Time `json:"at"`      // first press
	LastAt  time.Time `json:"last_at"` // latest press
	Presses int       `json:"presses"` // how many times (pressing again = waiting a while)

	// Calls from a guest's phone (the table's QR page).
	Guest  int    `json:"guest,omitempty"`  // who called (0 = the button on the table)
	Name   string `json:"name,omitempty"`   // their name, if they gave one
	Waiter bool   `json:"waiter,omitempty"` // "call the waiter", even during the 3 meals deal
}

// What a press meant.
const (
	PressNext   = "next"   // finished a meal, ready for the next one
	PressLast   = "last"   // finished the last meal: clear the table
	PressWaiter = "waiter" // no deal on this table: please come over
)

// PressResult tells the button (and the staff screen) what happened.
type PressResult struct {
	Table int    `json:"table"`
	Kind  string `json:"kind"`           // PressNext, PressLast or PressWaiter
	Meal  int    `json:"meal,omitempty"` // the meal they finished (deal only)
	Again bool   `json:"again"`          // the table was already waiting
}

// TableNow is one table on the staff Tables screen.
type TableNow struct {
	No   int
	Room string
	Run  *MealRun // the deal on this table, nil if none
	Call *Call    // an unanswered press, nil if none
}

// ensureButtons gives every table a button code (new data files, or more
// tables than before). Callers hold s.mu.
func (s *Store) ensureButtons() bool {
	have := map[int]bool{}
	for _, b := range s.d.Buttons {
		have[b.Table] = true
	}
	added := false
	for _, t := range schedule.TableNos() {
		if !have[t] {
			s.d.Buttons = append(s.d.Buttons, &Button{Table: t, Token: newRef()})
			added = true
		}
	}
	sort.SliceStable(s.d.Buttons, func(i, j int) bool {
		return schedule.TableOrder(s.d.Buttons[i].Table) < schedule.TableOrder(s.d.Buttons[j].Table)
	})
	return added
}

// buttonFor finds the button with this code. Every code is compared in full
// (constant time), so response times give nothing away. Callers hold s.mu.
func (s *Store) buttonFor(token string) *Button {
	var found *Button
	for _, b := range s.d.Buttons {
		if len(token) == len(b.Token) && subtle.ConstantTimeCompare([]byte(token), []byte(b.Token)) == 1 {
			found = b
		}
	}
	return found
}

func validTable(t int) bool { return schedule.ValidTable(t) }

// activeRun is the deal running on a table. Callers hold s.mu.
func (s *Store) activeRun(table int) *MealRun {
	for _, m := range s.d.MealRuns {
		if m.Table == table && m.Ended == nil {
			return m
		}
	}
	return nil
}

// callAt is the unanswered press on a table. Callers hold s.mu.
func (s *Store) callAt(table int) (int, *Call) {
	for i, c := range s.d.Calls {
		if c.Table == table {
			return i, c
		}
	}
	return -1, nil
}

// dropCall removes the unanswered press on a table. Callers hold s.mu.
func (s *Store) dropCall(table int) {
	if i, _ := s.callAt(table); i >= 0 {
		s.d.Calls = append(s.d.Calls[:i], s.d.Calls[i+1:]...)
	}
}

// change runs fn under the lock and saves if it reports a change. If saving
// fails, the tables part of the data is put back as it was.
func (s *Store) change(fn func() (bool, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.snapshotTables()
	changed, err := fn()
	if err != nil || !changed {
		s.restoreTables(before)
		return err
	}
	if err := s.save(); err != nil {
		s.restoreTables(before)
		return err
	}
	return nil
}

type tablesSnapshot struct {
	buttons []Button
	runs    []MealRun
	calls   []Call
	nextRun int
}

func (s *Store) snapshotTables() tablesSnapshot {
	var t tablesSnapshot
	for _, b := range s.d.Buttons {
		t.buttons = append(t.buttons, *b)
	}
	for _, m := range s.d.MealRuns {
		t.runs = append(t.runs, m.clone())
	}
	for _, c := range s.d.Calls {
		t.calls = append(t.calls, *c)
	}
	t.nextRun = s.d.NextRunID
	return t
}

func (s *Store) restoreTables(t tablesSnapshot) {
	s.d.Buttons, s.d.MealRuns, s.d.Calls = nil, nil, nil
	for i := range t.buttons {
		b := t.buttons[i]
		s.d.Buttons = append(s.d.Buttons, &b)
	}
	for i := range t.runs {
		m := t.runs[i]
		s.d.MealRuns = append(s.d.MealRuns, &m)
	}
	for i := range t.calls {
		c := t.calls[i]
		s.d.Calls = append(s.d.Calls, &c)
	}
	s.d.NextRunID = t.nextRun
}

// ButtonCheckIn records that a button is alive, without calling anyone: the
// button wakes once a day to report its battery.
func (s *Store) ButtonCheckIn(token string, battery int, now time.Time) (int, error) {
	table := 0
	err := s.change(func() (bool, error) {
		b := s.buttonFor(token)
		if b == nil {
			return false, ErrNoButton
		}
		t := now
		b.LastSeen = &t
		if battery > 0 && battery <= 100 {
			b.Battery = battery
		}
		table = b.Table
		return true, nil
	})
	return table, err
}

// Press records a press of the button with this code. battery is the
// button's charge in percent (0 = it didn't say).
func (s *Store) Press(token string, battery int, now time.Time) (PressResult, error) {
	var res PressResult
	err := s.changeDeal(func() (bool, error) {
		b := s.buttonFor(token)
		if b == nil {
			return false, ErrNoButton
		}
		t := now
		b.LastSeen = &t
		if battery > 0 && battery <= 100 {
			b.Battery = battery
		}
		res.Table = b.Table
		res.Kind = PressWaiter
		run := s.activeRun(b.Table)
		if run != nil {
			res.Meal = run.Meal()
			res.Kind = PressNext
			if run.LastMeal() {
				res.Kind = PressLast
			}
		}
		if _, c := s.callAt(b.Table); c != nil {
			res.Again = true
			if now.Sub(c.LastAt) >= PressGap {
				c.Presses++
				c.LastAt = now
			}
			return true, nil
		}
		if run != nil && !run.Waiting() {
			run.Ready = append(run.Ready, now)
			s.sendMeal(run, run.Meal()+1, now) // the chef starts the next meal
		}
		s.d.Calls = append(s.d.Calls, &Call{Table: b.Table, At: now, LastAt: now, Presses: 1})
		return true, nil
	})
	return res, err
}

// StartDeal starts the "3 meals" deal on a table: the first meal is on the
// table now.
func (s *Store) StartDeal(table int, now time.Time) error {
	return s.change(func() (bool, error) {
		_, err := s.startRun(table, now)
		return err == nil, err
	})
}

// startRun begins the deal on a table. Callers hold s.mu.
func (s *Store) startRun(table int, now time.Time) (*MealRun, error) {
	if !validTable(table) {
		return nil, ErrNoTable
	}
	if s.activeRun(table) != nil {
		return nil, ErrDealRunning
	}
	s.d.NextRunID++
	r := &MealRun{ID: s.d.NextRunID, Table: table, Date: schedule.FormatDate(schedule.Today(now)), Served: []time.Time{now}}
	s.d.MealRuns = append(s.d.MealRuns, r)
	s.dropCall(table) // staff are at the table anyway
	return r, nil
}

// ErrStale: the table changed since the screen was drawn (another waiter
// was quicker), so the tap is ignored rather than serving a meal twice.
var ErrStale = errors.New("the table changed in the meantime")

// ServeNext records that meal number meal is on the table (and the plates of
// the last one are cleared). It works with or without a button press first:
// staff may see that a table has finished before anyone presses. meal 0
// means "whichever is next".
func (s *Store) ServeNext(table, meal int, now time.Time) error {
	return s.changeDeal(func() (bool, error) {
		run := s.activeRun(table)
		if run == nil {
			return false, ErrNoDeal
		}
		if run.LastMeal() {
			return false, ErrLastMeal
		}
		if meal != 0 && meal != run.Meal()+1 {
			return false, ErrStale
		}
		if !run.Waiting() {
			run.Ready = append(run.Ready, now)
		}
		run.Served = append(run.Served, now)
		s.dropCall(table)
		s.dealTicketsOut(run.ID, run.Meal(), now) // it's on the table: off the kitchen screen
		return true, nil
	})
}

// EndDeal clears the table after the last meal, or stops the deal early.
func (s *Store) EndDeal(table int, now time.Time) error {
	return s.changeDeal(func() (bool, error) {
		run := s.activeRun(table)
		if run == nil {
			return false, ErrNoDeal
		}
		if run.LastMeal() {
			if !run.Waiting() {
				run.Ready = append(run.Ready, now)
			}
		} else {
			run.Stopped = true
		}
		t := now
		run.Ended = &t
		s.dropCall(table)
		s.dealTicketsOut(run.ID, 0, now) // checked out: the kitchen is done with this table
		return true, nil
	})
}

// NotYet undoes a press during the deal that wasn't meant (a child playing
// with the button, guests still eating): the table goes back to "eating".
func (s *Store) NotYet(table int) error {
	return s.changeDeal(func() (bool, error) {
		_, c := s.callAt(table)
		if c == nil {
			return false, nil
		}
		if run := s.activeRun(table); run != nil && run.Waiting() && len(run.Ready) > 0 {
			run.Ready = run.Ready[:len(run.Ready)-1]
			s.cancelMeal(run.ID, run.Meal()+1) // the chef can stop
		}
		s.dropCall(table)
		return true, nil
	})
}

// Answer marks a "wants a waiter" press as handled.
func (s *Store) Answer(table int) error {
	return s.change(func() (bool, error) {
		_, c := s.callAt(table)
		if c == nil {
			return false, nil
		}
		if run := s.activeRun(table); run != nil && !c.Waiter {
			// During the deal a press is answered by serving or clearing.
			return false, ErrDealRunning
		}
		s.dropCall(table)
		return true, nil
	})
}

// TablesNow is every table with its deal and unanswered press, in table order.
func (s *Store) TablesNow() []TableNow {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TableNow, schedule.TableCount())
	for i, t := range schedule.Tables() {
		out[i].No, out[i].Room = t.No, t.Room
	}
	for _, m := range s.d.MealRuns {
		if i := schedule.TableOrder(m.Table); m.Ended == nil && i >= 0 {
			c := m.clone()
			out[i].Run = &c
		}
	}
	for _, c := range s.d.Calls {
		if i := schedule.TableOrder(c.Table); i >= 0 {
			cc := *c
			out[i].Call = &cc
		}
	}
	return out
}

// Buttons lists the table buttons with their codes (for setting them up).
func (s *Store) Buttons() []Button {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Button, 0, len(s.d.Buttons))
	for _, b := range s.d.Buttons {
		c := *b
		if c.LastSeen != nil {
			t := *c.LastSeen
			c.LastSeen = &t
		}
		out = append(out, c)
	}
	return out
}

// NewButtonCode gives a table's button a new secret code (if a button is
// lost or stolen). The old code stops working at once.
func (s *Store) NewButtonCode(table int) (Button, error) {
	var out Button
	err := s.change(func() (bool, error) {
		for _, b := range s.d.Buttons {
			if b.Table == table {
				b.Token = newRef()
				b.LastSeen, b.Battery = nil, 0
				out = *b
				return true, nil
			}
		}
		return false, ErrNoTable
	})
	return out, err
}

// PressTable presses a table's button from inside the program (the test
// buttons in demo mode), exactly as the real button would.
func (s *Store) PressTable(table int, now time.Time) (PressResult, error) {
	s.mu.Lock()
	token := ""
	for _, b := range s.d.Buttons {
		if b.Table == table {
			token = b.Token
		}
	}
	s.mu.Unlock()
	if token == "" {
		return PressResult{}, ErrNoTable
	}
	return s.Press(token, 0, now)
}

// TidyTables ends deals left open from an earlier day and forgets presses
// nobody answered for hours, so each service starts clean.
func (s *Store) TidyTables(now time.Time) error {
	today := schedule.FormatDate(schedule.Today(now))
	return s.change(func() (bool, error) {
		changed := false
		for _, m := range s.d.MealRuns {
			if m.Ended == nil && m.Date < today {
				end := m.Since().Add(time.Hour)
				if end.After(now) {
					end = now
				}
				m.Ended, m.Stopped = &end, !(m.LastMeal() && m.Waiting())
				changed = true
			}
		}
		kept := s.d.Calls[:0]
		for _, c := range s.d.Calls {
			if now.Sub(c.LastAt) < 4*time.Hour {
				kept = append(kept, c)
			} else {
				changed = true
			}
		}
		s.d.Calls = kept
		return changed, nil
	})
}

// DealStats is how the deal went on one day.
type DealStats struct {
	Tables   int           // tables that had the deal
	Finished int           // tables that got through all the meals
	Eat      time.Duration // average time to eat a meal (served -> button)
	Wait     time.Duration // average wait after the button (button -> next meal / cleared)
	Waits    int           // how many waits the average is based on
}

// DealStatsOn works out the day's deal figures.
func (s *Store) DealStatsOn(date string, now time.Time) DealStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st DealStats
	var eat, wait time.Duration
	eats := 0
	for _, m := range s.d.MealRuns {
		if m.Date != date {
			continue
		}
		st.Tables++
		if m.Ended != nil && !m.Stopped {
			st.Finished++
		}
		for i, r := range m.Ready {
			if i < len(m.Served) {
				eat += r.Sub(m.Served[i])
				eats++
			}
			switch {
			case i+1 < len(m.Served):
				wait += m.Served[i+1].Sub(r)
				st.Waits++
			case i == schedule.MealsInDeal-1 && m.Ended != nil && !m.Stopped:
				wait += m.Ended.Sub(r)
				st.Waits++
			}
		}
	}
	if eats > 0 {
		st.Eat = eat / time.Duration(eats)
	}
	if st.Waits > 0 {
		st.Wait = wait / time.Duration(st.Waits)
	}
	return st
}
