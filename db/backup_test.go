package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"emeal-booking/schedule"
)

// A backup is a full copy that opens like the real file, there's one per
// day, and only the newest ones are kept (other files are left alone).
func TestDailyBackups(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "emeal.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 11, 1, 3, 0, 0, 0, schedule.Location)
	if HasBackup(dir, day) {
		t.Fatal("backup before any was made")
	}
	var last string
	for i := 0; i < 5; i++ {
		if last, err = s.Backup(dir, 3, day.AddDate(0, 0, i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Backup(dir, 3, day.AddDate(0, 0, 4).Add(time.Hour)); err != nil { // same day again
		t.Fatal(err)
	}
	if !HasBackup(dir, day.AddDate(0, 0, 4)) {
		t.Fatal("today's backup missing")
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"emeal-2026-11-03.json", "emeal-2026-11-04.json", "emeal-2026-11-05.json", "notes.txt"}
	if len(names) != len(want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("files %v, want %v", names, want)
		}
	}
	if _, err := Open(last); err != nil {
		t.Fatalf("a backup should open like the real file: %v", err)
	}
}
