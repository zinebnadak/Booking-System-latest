package schedule

import (
	"testing"
	"time"
)

// A Monday far in the past, so "now" never filters anything out.
var longAgo = time.Date(2000, 1, 1, 0, 0, 0, 0, Location)

func day(s string) time.Time {
	d, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestTableSlotsWeekday(t *testing.T) {
	s := TableSlots(day("2026-10-26"), longAgo) // Monday
	if s[0] != "10:30" || s[len(s)-1] != "19:00" {
		t.Fatalf("Monday slots = %v .. %v, want 10:30 .. 19:00", s[0], s[len(s)-1])
	}
	if len(s) != 18 { // 10:30..19:00 every 30 min
		t.Fatalf("got %d slots, want 18", len(s))
	}
}

func TestTableSlotsFridaySaturdaySunday(t *testing.T) {
	fri := TableSlots(day("2026-10-23"), longAgo)
	if fri[len(fri)-1] != "20:00" {
		t.Errorf("Friday last slot = %s, want 20:00", fri[len(fri)-1])
	}
	sat := TableSlots(day("2026-10-24"), longAgo)
	if sat[0] != "16:00" || sat[len(sat)-1] != "20:00" {
		t.Errorf("Saturday slots = %v", sat)
	}
	if sun := TableSlots(day("2026-10-25"), longAgo); len(sun) == 0 || sun[0] != "12:00" || sun[len(sun)-1] != "18:00" {
		t.Errorf("Sunday slots = %v, want 12:00 to 18:00", sun)
	}
}

func TestTableSlotsLeadTime(t *testing.T) {
	d := day("2026-10-26")
	now, _ := At(d, "17:10") // 1h lead time -> first slot 18:30
	s := TableSlots(d, now)
	if len(s) == 0 || s[0] != "18:30" {
		t.Fatalf("first slot after 17:10 = %v, want 18:30", s)
	}
}

func TestPickupSlots(t *testing.T) {
	d := day("2026-10-26") // Monday: kitchen 10:30–20:00
	s := PickupSlots(d, longAgo)
	if s[0] != "11:00" || s[len(s)-1] != "20:00" {
		t.Fatalf("pickup = %s .. %s, want 11:00 .. 20:00", s[0], s[len(s)-1])
	}
	now, _ := At(d, "12:05") // +30 min -> 12:35 -> first slot 12:45
	if s := PickupSlots(d, now); s[0] != "12:45" {
		t.Fatalf("first pickup after 12:05 = %s, want 12:45", s[0])
	}
}

func TestOverlaps(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"18:00", "18:00", true},
		{"18:00", "19:30", true},
		{"18:00", "20:00", false}, // table is free again at 20:00
		{"16:00", "18:00", false},
		{"16:30", "18:00", true},
	}
	for _, c := range cases {
		if got := Overlaps(c.a, c.b); got != c.want {
			t.Errorf("Overlaps(%s,%s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestDeposits(t *testing.T) {
	for size, want := range map[int]int{1: 1000, 2: 1000, 3: 2000, 6: 2000} {
		if got := TableDeposit(size); got != want {
			t.Errorf("TableDeposit(%d) = %d, want %d", size, got, want)
		}
	}
	for total, want := range map[int]int{4590: 1148, 2290: 573, 100: 50, 3990: 998} {
		if got := TakeawayDeposit(total); got != want {
			t.Errorf("TakeawayDeposit(%d) = %d, want %d", total, got, want)
		}
	}
}

func TestRefundEligible(t *testing.T) {
	d := day("2026-10-30")
	start, _ := At(d, "18:00")
	if !RefundEligible("2026-10-30", "18:00", start.Add(-25*time.Hour)) {
		t.Error("25h before should be refundable")
	}
	if RefundEligible("2026-10-30", "18:00", start.Add(-23*time.Hour)) {
		t.Error("23h before should not be refundable")
	}
}

func TestOverlapsFor(t *testing.T) {
	cases := []struct {
		a    string
		aMin int
		b    string
		bMin int
		want bool
	}{
		{"18:00", 0, "19:59", 0, true},
		{"18:00", 0, "20:00", 0, false},
		{"12:05", 55, "13:00", 0, false}, // a walk-in until the next booking
		{"12:05", 56, "13:00", 0, true},
		{"13:00", 0, "12:05", 55, false},
		{"14:00", 30, "13:00", 0, true},
	}
	for _, c := range cases {
		if got := OverlapsFor(c.a, c.aMin, c.b, c.bMin); got != c.want {
			t.Errorf("OverlapsFor(%s,%d,%s,%d) = %v", c.a, c.aMin, c.b, c.bMin, got)
		}
	}
	if EndTimeFor("12:05", 55) != "13:00" || EndTime("18:00") != "20:00" {
		t.Fatal("EndTimeFor")
	}
}
