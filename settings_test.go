package main

import (
	"strings"
	"testing"

	"ozmo-booking/handlers"
)

func TestIsLocal(t *testing.T) {
	for url, want := range map[string]bool{
		"http://localhost:8080":    true,
		"http://127.0.0.1:8080":    true,
		"http://[::1]:8080":        true,
		"https://book.pizza.ax":    false,
		"http://192.168.1.88:8080": false,
		"http://localhost.evil.ax": false,
	} {
		if got := isLocal(url); got != want {
			t.Errorf("isLocal(%s) = %v", url, got)
		}
	}
}

func TestCheckSettings(t *testing.T) {
	cfg := func(base, admin, owner string) *handlers.Config {
		return &handlers.Config{BaseURL: base, AdminUser: "admin", AdminPassword: admin, OwnerUser: "owner", OwnerPassword: owner}
	}
	strong1, strong2 := "correct-horse-battery", "staple-lantern-river"

	// Your own computer, no Stripe: the demo login is filled in. There's one
	// manager now; the second login (OWNER_*) stays off unless it's set.
	c := cfg("http://localhost:8080", "", "")
	if err := checkSettings(c, false, false); err != nil || c.AdminPassword != "admin" || c.OwnerPassword != "" {
		t.Fatalf("local demo: %v %+v", err, c)
	}
	// Public address: no demo logins, no fake payments by accident.
	mustFail := func(name string, c *handlers.Config, stripe, demo bool, want string) {
		t.Helper()
		err := checkSettings(c, stripe, demo)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want error containing %q", name, err, want)
		}
	}
	mustFail("public without stripe", cfg("https://book.pizza.ax", strong1, strong2), false, false, "DEMO_MODE")
	mustFail("public demo without passwords", cfg("https://book.pizza.ax", "", ""), false, true, "must be set")
	mustFail("stripe without passwords", cfg("https://book.pizza.ax", "", strong2), true, false, "must be set")
	mustFail("short password", cfg("https://book.pizza.ax", "hunter2", strong2), true, false, "too short")
	mustFail("short second login", cfg("https://book.pizza.ax", strong1, "hunter2"), true, false, "OWNER_PASSWORD is too short")
	if err := checkSettings(cfg("https://book.pizza.ax", strong1, ""), true, false); err != nil {
		t.Fatalf("one manager login refused: %v", err)
	}
	same := cfg("https://book.pizza.ax", strong1, strong2)
	same.OwnerUser = "Admin"
	mustFail("same usernames", same, true, false, "ADMIN_USER and OWNER_USER")

	ok := cfg("https://book.pizza.ax", strong1, strong2)
	if err := checkSettings(ok, true, false); err != nil {
		t.Fatalf("good live settings refused: %v", err)
	}
	if err := checkSettings(cfg("https://demo.example.ax", strong1, strong2), false, true); err != nil {
		t.Fatalf("public demo with DEMO_MODE=1 and passwords refused: %v", err)
	}
	// A guessable calendar token switches the calendar link off.
	weak := cfg("https://book.pizza.ax", strong1, strong2)
	weak.CalendarToken = "abc"
	if err := checkSettings(weak, true, false); err != nil || weak.CalendarToken != "" {
		t.Fatalf("weak calendar token kept: %v %q", err, weak.CalendarToken)
	}
	good := cfg("https://book.pizza.ax", strong1, strong2)
	good.CalendarToken = strings.Repeat("a1", 16)
	if err := checkSettings(good, true, false); err != nil || good.CalendarToken == "" {
		t.Fatalf("good calendar token dropped: %v", err)
	}
}
