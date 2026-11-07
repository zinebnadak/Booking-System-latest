package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"emeal-booking/schedule"
)

func at(clock string) time.Time {
	d, _ := schedule.ParseDate("2026-10-31") // a Saturday
	t, _ := schedule.At(d, clock)
	return t
}

func tokenOf(t *testing.T, s *Store, table int) string {
	t.Helper()
	for _, b := range s.Buttons() {
		if b.Table == table {
			return b.Token
		}
	}
	t.Fatalf("no button for table %d", table)
	return ""
}

func TestEveryTableHasASecretButton(t *testing.T) {
	s := newStore(t)
	bs := s.Buttons()
	if len(bs) != schedule.TableCount() {
		t.Fatalf("%d buttons, want %d", len(bs), schedule.TableCount())
	}
	seen := map[string]bool{}
	for i, b := range bs {
		if b.Table != i+1 || len(b.Token) != 32 || seen[b.Token] {
			t.Fatalf("button %+v", b)
		}
		seen[b.Token] = true
	}
	if _, err := s.Press("not-a-code", 0, at("18:00")); !errors.Is(err, ErrNoButton) {
		t.Fatalf("wrong code: %v", err)
	}
	if _, err := s.Press("", 0, at("18:00")); !errors.Is(err, ErrNoButton) {
		t.Fatalf("empty code: %v", err)
	}
}

func TestThreeMealsFlow(t *testing.T) {
	s := newStore(t)
	tok := tokenOf(t, s, 4)

	// Before the deal starts, the button calls a waiter.
	r, err := s.Press(tok, 87, at("17:58"))
	if err != nil || r.Table != 4 || r.Kind != PressWaiter || r.Again {
		t.Fatalf("waiter press: %+v %v", r, err)
	}
	if err := s.StartDeal(4, at("18:00")); err != nil { // starting it answers the call
		t.Fatal(err)
	}
	if err := s.StartDeal(4, at("18:01")); !errors.Is(err, ErrDealRunning) {
		t.Fatalf("second start: %v", err)
	}
	tbl := s.TablesNow()[3]
	if tbl.Call != nil || tbl.Run == nil || tbl.Run.Meal() != 1 || tbl.Run.Waiting() {
		t.Fatalf("after start: %+v", tbl)
	}

	// Meal 1 finished: press -> ready for meal 2.
	r, _ = s.Press(tok, 0, at("18:20"))
	if r.Kind != PressNext || r.Meal != 1 || r.Again {
		t.Fatalf("meal 1 press: %+v", r)
	}
	// A bounce within 3 seconds doesn't count; a press a minute later does.
	s.Press(tok, 0, at("18:20").Add(time.Second))
	r, _ = s.Press(tok, 0, at("18:21"))
	tbl = s.TablesNow()[3]
	if !r.Again || tbl.Call == nil || tbl.Call.Presses != 2 || !tbl.Run.Waiting() {
		t.Fatalf("pressed again: %+v %+v", r, tbl.Call)
	}
	if err := s.Answer(4); !errors.Is(err, ErrDealRunning) {
		t.Fatalf("answer during the deal: %v", err)
	}

	// Meal 2 served at 18:23 (3 minutes after the first press).
	if err := s.ServeNext(4, 2, at("18:23")); err != nil {
		t.Fatal(err)
	}
	tbl = s.TablesNow()[3]
	if tbl.Call != nil || tbl.Run.Meal() != 2 || tbl.Run.Waiting() {
		t.Fatalf("after meal 2: %+v", tbl)
	}

	// A second waiter taps "Meal 2 served" on a screen that wasn't updated yet.
	if err := s.ServeNext(4, 2, at("18:24")); !errors.Is(err, ErrStale) {
		t.Fatalf("double serve: %v", err)
	}

	// A child presses during meal 2: staff say "not yet".
	s.Press(tok, 0, at("18:30"))
	if err := s.NotYet(4); err != nil {
		t.Fatal(err)
	}
	if tbl = s.TablesNow()[3]; tbl.Call != nil || tbl.Run.Waiting() {
		t.Fatalf("after not yet: %+v", tbl)
	}

	// Staff see the table is done without a press, and bring meal 3.
	if err := s.ServeNext(4, 0, at("18:45")); err != nil {
		t.Fatal(err)
	}
	if err := s.ServeNext(4, 0, at("18:46")); !errors.Is(err, ErrLastMeal) {
		t.Fatalf("a 4th meal: %v", err)
	}
	r, _ = s.Press(tok, 0, at("19:05"))
	if r.Kind != PressLast || r.Meal != 3 {
		t.Fatalf("last press: %+v", r)
	}
	if err := s.EndDeal(4, at("19:07")); err != nil {
		t.Fatal(err)
	}
	if tbl = s.TablesNow()[3]; tbl.Run != nil || tbl.Call != nil {
		t.Fatalf("after clearing: %+v", tbl)
	}

	// Figures: eating 20, 22, 20 min -> 20m40s; waits 3, 0, 2 min -> 1m40s.
	st := s.DealStatsOn("2026-10-31", at("20:00"))
	if st.Tables != 1 || st.Finished != 1 || st.Waits != 3 || st.Eat != 20*time.Minute+40*time.Second || st.Wait != 100*time.Second {
		t.Fatalf("stats: %+v", st)
	}
	// The button's battery and last signal were remembered.
	b := s.Buttons()[3]
	if b.Battery != 87 || b.LastSeen == nil || !b.LastSeen.Equal(at("19:05")) {
		t.Fatalf("button: %+v", b)
	}
}

func TestStopDealEarlyAndTidy(t *testing.T) {
	s := newStore(t)
	s.StartDeal(2, at("18:00"))
	if err := s.EndDeal(2, at("18:10")); err != nil {
		t.Fatal(err)
	}
	if st := s.DealStatsOn("2026-10-31", at("20:00")); st.Tables != 1 || st.Finished != 0 {
		t.Fatalf("stopped deal counted as finished: %+v", st)
	}

	// A deal and a press left open overnight are cleaned up the next day.
	s.StartDeal(3, at("21:00"))
	s.PressTable(5, at("21:30"))
	next := at("21:00").Add(14 * time.Hour)
	if err := s.TidyTables(next); err != nil {
		t.Fatal(err)
	}
	for _, tb := range s.TablesNow() {
		if tb.Run != nil || tb.Call != nil {
			t.Fatalf("left over after tidying: %+v", tb)
		}
	}
}

func TestCheckIn(t *testing.T) {
	s := newStore(t)
	if n, err := s.ButtonCheckIn(tokenOf(t, s, 2), 64, at("03:00")); err != nil || n != 2 {
		t.Fatalf("check-in: %d %v", n, err)
	}
	if b := s.Buttons()[1]; b.Battery != 64 || b.LastSeen == nil {
		t.Fatalf("button: %+v", b)
	}
	if tb := s.TablesNow()[1]; tb.Call != nil {
		t.Fatal("a check-in must not call a waiter")
	}
	if _, err := s.ButtonCheckIn("nope", 50, at("03:00")); !errors.Is(err, ErrNoButton) {
		t.Fatal(err)
	}
}

func TestNewButtonCode(t *testing.T) {
	s := newStore(t)
	old := tokenOf(t, s, 1)
	b, err := s.NewButtonCode(1)
	if err != nil || b.Token == old || len(b.Token) != 32 {
		t.Fatalf("new code: %+v %v", b, err)
	}
	if _, err := s.Press(old, 0, at("18:00")); !errors.Is(err, ErrNoButton) {
		t.Fatal("the old code still works")
	}
	if r, err := s.Press(b.Token, 0, at("18:00")); err != nil || r.Table != 1 {
		t.Fatalf("new code: %+v %v", r, err)
	}
}

func TestButtonsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	tok := tokenOf(t, s, 6)
	s.StartDeal(6, at("18:00"))
	s.Press(tok, 0, at("18:25"))

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if tokenOf(t, s2, 6) != tok {
		t.Fatal("button code changed on restart")
	}
	tb := s2.TablesNow()[5]
	if tb.Run == nil || tb.Call == nil || !tb.Run.Waiting() {
		t.Fatalf("lost after restart: %+v", tb)
	}
	if err := s2.StartDeal(1, at("18:30")); err != nil {
		t.Fatal(err)
	}
	if runs := s2.TablesNow(); runs[0].Run == nil || runs[0].Run.ID == runs[5].Run.ID {
		t.Fatal("deal ids repeat after restart")
	}
}
