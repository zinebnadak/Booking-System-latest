package db

import (
	"encoding/hex"
	"errors"
	"testing"
)

func TestPBKDF2KnownAnswer(t *testing.T) {
	// RFC 7914, section 11: PBKDF2-HMAC-SHA256("passwd", "salt", 1, 64).
	want := "55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc49ca9cccf179b645991664b39d77ef317c71b845b1e30bd509112041d3a19783"
	if got := hex.EncodeToString(pbkdf2SHA256([]byte("passwd"), []byte("salt"), 1, 64)); got != want {
		t.Fatalf("got %s", got)
	}
	// And with 80000 rounds ("Password", "NaCl").
	want = "4ddcd8f60b98be21830cee5ef22701f9641a4418d04c0414aeff08876b34ab56a1d425a1225833549adb841b51c9b3176a272bdebba1d078478f62b397f33c8d"
	if got := hex.EncodeToString(pbkdf2SHA256([]byte("Password"), []byte("NaCl"), 80000, 64)); got != want {
		t.Fatalf("got %s", got)
	}
}

func TestWaiterLogins(t *testing.T) {
	s := newStore(t)
	if _, err := s.AddStaff("Anna", "anna", "short", at("10:00")); !errors.Is(err, ErrBadStaff) {
		t.Fatalf("short password: %v", err)
	}
	a, err := s.AddStaff("Anna", " Anna ", "correct horse", at("10:00"))
	if err != nil || a.Username != "anna" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := s.AddStaff("Anna B", "anna", "another one", at("10:00")); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("same username: %v", err)
	}
	if _, ok := s.CheckStaffLogin("ANNA", "correct horse"); !ok {
		t.Fatal("right password refused")
	}
	if _, ok := s.CheckStaffLogin("anna", "wrong password"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, ok := s.CheckStaffLogin("nobody", "correct horse"); ok {
		t.Fatal("unknown user accepted")
	}
	if _, ok := s.StaffSession(a.ID, a.Ver); !ok {
		t.Fatal("session refused")
	}
	// A new password logs her out everywhere.
	_ = s.NewStaffPassword(a.ID, "battery staple")
	if _, ok := s.StaffSession(a.ID, a.Ver); ok {
		t.Fatal("old login still works after a new password")
	}
	if _, ok := s.CheckStaffLogin("anna", "battery staple"); !ok {
		t.Fatal("new password refused")
	}
	// Removed: can't log in, and the username is free again.
	_ = s.RemoveStaff(a.ID)
	if _, ok := s.CheckStaffLogin("anna", "battery staple"); ok {
		t.Fatal("removed waiter can log in")
	}
	if len(s.StaffList()) != 0 {
		t.Fatal("removed waiter still listed")
	}
	if _, err := s.AddStaff("Anna", "anna", "correct horse", at("11:00")); err != nil {
		t.Fatalf("username free again: %v", err)
	}
	// Nothing in the data file can be read as a password.
	for _, m := range s.d.Staff {
		if m.Pass == "correct horse" || m.Pass == "battery staple" {
			t.Fatal("password stored as plain text")
		}
	}
}
