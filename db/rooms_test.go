package db

import (
	"testing"
	"time"

	"emeal-booking/schedule"
)

func TestRealTablesBestFit(t *testing.T) {
	realTables(t)
	s := newStore(t)
	now := time.Now()
	// A couple gets a 2-seat table, a party of 6 a 6-seat one.
	two, err := s.CreateTableBooking(Booking{Date: "2026-11-06", Time: "18:00", PartySize: 2, Email: "a@x.fi"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := schedule.TableInfo(two.TableNo); info.Seats != 2 {
		t.Fatalf("couple got table %d with %d seats", two.TableNo, info.Seats)
	}
	six, err := s.CreateTableBooking(Booking{Date: "2026-11-06", Time: "18:00", PartySize: 6, Email: "b@x.fi"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := schedule.TableInfo(six.TableNo); info.Seats != 6 {
		t.Fatalf("party of 6 got table %d with %d seats", six.TableNo, info.Seats)
	}
	// Every table is somewhere in the plan, and the counts add up.
	if schedule.TableCount() != 50 || !schedule.ValidTable(151) || schedule.ValidTable(8) == false || schedule.ValidTable(40) {
		t.Fatalf("plan: %d tables", schedule.TableCount())
	}
	free := s.FreeTables("2026-11-06", []string{"18:00"}, 6, true, now)["18:00"]
	all6 := 0
	for _, tb := range schedule.TablePlan {
		if tb.Seats >= 6 {
			all6++
		}
	}
	if free != all6-1 {
		t.Fatalf("tables for 6 still free = %d, want %d", free, all6-1)
	}
}

func TestRoomClosedOnline(t *testing.T) {
	realTables(t)
	s := newStore(t)
	now := time.Now()
	before := s.FreeTables("2026-11-06", []string{"18:00"}, 2, true, now)["18:00"]
	if err := s.SetRoomOnline("Terrassen", false); err != nil {
		t.Fatal(err)
	}
	terrace := 0
	for _, tb := range schedule.TablePlan {
		if tb.Room == "Terrassen" {
			terrace++
		}
	}
	if after := s.FreeTables("2026-11-06", []string{"18:00"}, 2, true, now)["18:00"]; after != before-terrace {
		t.Fatalf("online after closing the terrace = %d, want %d", after, before-terrace)
	}
	// Staff still see (and can use) every table.
	if staff := s.FreeTables("2026-11-06", []string{"18:00"}, 2, false, now)["18:00"]; staff != before {
		t.Fatalf("staff free = %d, want %d", staff, before)
	}
	// Online bookings never land on the terrace.
	for i := 0; i < 40; i++ {
		b, err := s.CreateTableBooking(Booking{Date: "2026-11-06", Time: "18:00", PartySize: 2, Phone: "04001234" + string(rune('0'+i%10)) + string(rune('0'+i/10))}, now)
		if err != nil {
			break
		}
		if info, _ := schedule.TableInfo(b.TableNo); info.Room == "Terrassen" {
			t.Fatalf("booking %d got terrace table %d", i, b.TableNo)
		}
	}
	if err := s.SetRoomOnline("Kitchen", true); err == nil {
		t.Fatal("unknown room accepted")
	}
	if err := s.SetRoomOnline("Terrassen", true); err != nil || len(s.RoomsOffline()) != 0 {
		t.Fatalf("reopen: %v %v", err, s.RoomsOffline())
	}
}
