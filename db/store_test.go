package db

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"emeal-booking/schedule"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSeedMenu(t *testing.T) {
	s := newStore(t)
	if n := len(s.Menu(true)); n != 14 {
		t.Fatalf("seeded %d dishes and drinks, want 14", n)
	}
	if n := len(s.TakeawayMenu()); n != 10 {
		t.Fatalf("takeaway menu has %d dishes, want 10 (no drinks)", n)
	}
}

func TestNoDoubleBooking(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	// 20 guests try to book 18:00 at the same moment: only 6 tables exist.
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateTableBooking(Booking{Date: "2026-10-30", Time: "18:00", PartySize: 2}, now)
			if err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != schedule.TableCount() {
		t.Fatalf("%d bookings succeeded, want %d", ok, schedule.TableCount())
	}
	// 19:30 overlaps 18:00 (2h bookings) -> full; 20:00 does not -> free.
	free := s.FreeTables("2026-10-30", []string{"19:30", "20:00"}, 0, true, now)
	if free["19:30"] != 0 || free["20:00"] != 6 {
		t.Fatalf("free = %v", free)
	}
}

func TestHoldExpires(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	b, err := s.CreateTableBooking(Booking{Date: "2026-10-30", Time: "18:00", PartySize: 4}, now)
	if err != nil {
		t.Fatal(err)
	}
	if f := s.FreeTables(b.Date, []string{"18:00"}, 0, true, now)["18:00"]; f != 5 {
		t.Fatalf("free while held = %d, want 5", f)
	}
	later := now.Add(schedule.HoldDuration + time.Minute)
	if f := s.FreeTables(b.Date, []string{"18:00"}, 0, true, later)["18:00"]; f != 6 {
		t.Fatalf("free after hold expired = %d, want 6", f)
	}
}

func TestMarkPaidOnlyOnce(t *testing.T) {
	s := newStore(t)
	now := time.Now()
	b, _ := s.CreateTakeaway(Booking{Date: "2026-10-30", Time: "12:00", TotalCents: 2000, DepositCents: 500}, now)
	if _, changed, _ := s.MarkPaid(b.Ref, "pi_1", now); !changed {
		t.Fatal("first MarkPaid should change")
	}
	if _, changed, _ := s.MarkPaid(b.Ref, "pi_1", now); changed {
		t.Fatal("second MarkPaid should not change")
	}
	// survives a restart
	s2, err := Open(s.path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s2.Get(b.Ref)
	if err != nil || got.Status != StatusPaid || got.PaymentIntentID != "pi_1" {
		t.Fatalf("after reload: %+v, %v", got, err)
	}
}
