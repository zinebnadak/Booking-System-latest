package db

import "testing"

func TestCleanBusinessID(t *testing.T) {
	for in, want := range map[string]string{
		"0112038-9":     "0112038-9", // a real one (Nokia)
		"01120389":      "0112038-9",
		"FI01120389":    "0112038-9",
		" 0112038 - 9 ": "0112038-9",
	} {
		if got, err := CleanBusinessID(in); err != nil || got != want {
			t.Errorf("%q: got %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"0112038-8", "123", "abcdefg-h", "1234567-2"} {
		if _, err := CleanBusinessID(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}
