package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"emeal-booking/schedule"
)

// ---------- Daily backups ----------

// backupPrefix starts every backup file's name: emeal-2026-11-09.json.
const backupPrefix = "emeal-"

// Backup writes a copy of everything to dir as emeal-<date>.json (one file
// per day: a second backup on the same day replaces the first), then keeps
// only the newest keep files. It returns the file it wrote.
func (s *Store) Backup(dir string, keep int, now time.Time) (string, error) {
	s.mu.Lock()
	raw, err := json.MarshalIndent(s.d, "", "  ")
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	name := filepath.Join(dir, backupPrefix+schedule.FormatDate(now)+".json")
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, name); err != nil {
		return "", err
	}
	return name, pruneBackups(dir, keep)
}

// HasBackup says whether today's backup is already in dir.
func HasBackup(dir string, now time.Time) bool {
	_, err := os.Stat(filepath.Join(dir, backupPrefix+schedule.FormatDate(now)+".json"))
	return err == nil
}

// pruneBackups deletes the oldest backups so only keep are left. Only our
// own files (emeal-<date>.json) are touched, never anything else in dir.
func pruneBackups(dir string, keep int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var ours []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(n, ".json") {
			if _, err := schedule.ParseDate(strings.TrimSuffix(strings.TrimPrefix(n, backupPrefix), ".json")); err == nil {
				ours = append(ours, n)
			}
		}
	}
	sort.Strings(ours) // dates sort oldest first
	for len(ours) > keep {
		if err := os.Remove(filepath.Join(dir, ours[0])); err != nil {
			return err
		}
		ours = ours[1:]
	}
	return nil
}
