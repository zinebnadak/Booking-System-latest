package db

import (
	"errors"
	"strings"
)

// ---------- The business ID on receipts ----------

// ErrBusinessID: not a Finnish business ID (Y-tunnus / FO-nummer), which
// looks like 1234567-8 and has a check digit.
var ErrBusinessID = errors.New("a business ID looks like 1234567-8")

// CleanBusinessID checks a business ID and writes it the standard way
// ("1234567-8"). "FI12345678" (the VAT number) is accepted too.
func CleanBusinessID(s string) (string, error) {
	s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
	s = strings.TrimPrefix(s, "FI")
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 8 {
		return "", ErrBusinessID
	}
	d := make([]int, 8)
	for i, c := range s {
		if c < '0' || c > '9' {
			return "", ErrBusinessID
		}
		d[i] = int(c - '0')
	}
	sum := 0
	for i, w := range []int{7, 9, 10, 5, 8, 4, 2} {
		sum += d[i] * w
	}
	check := 0
	switch r := sum % 11; r {
	case 0:
	case 1:
		return "", ErrBusinessID // no business ID has this
	default:
		check = 11 - r
	}
	if d[7] != check {
		return "", ErrBusinessID
	}
	return s[:7] + "-" + s[7:], nil
}

// SetBusinessID saves the business ID printed on receipts ("" removes it).
func (s *Store) SetBusinessID(id string) error {
	if id != "" {
		var err error
		if id, err = CleanBusinessID(id); err != nil {
			return err
		}
	}
	return s.changeOrders(func() (bool, error) {
		changed := s.d.Settings.BusinessID != id
		s.d.Settings.BusinessID = id
		return changed, nil
	})
}

// BusinessID is the business ID the manager saved ("" if none).
func (s *Store) BusinessID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Settings.BusinessID
}
