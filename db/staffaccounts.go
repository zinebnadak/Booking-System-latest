package db

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"emeal-booking/schedule"
)

// Waiters' own logins, and what staff did with money (the activity list).
//
// The manager adds each waiter with a username and password. Passwords are
// never stored: only a slow, salted fingerprint of them (PBKDF2-SHA256), so
// even someone who got hold of the data file couldn't read them. Removing a
// waiter, or giving them a new password, logs them out everywhere at once.

var (
	ErrUsernameTaken = errors.New("that username is taken")
	ErrBadStaff      = errors.New("name, username or password missing or too short")
)

// MinStaffPassword is the shortest password a waiter can have.
const MinStaffPassword = 8

const pbkdf2Rounds = 120_000

// StaffMember is a waiter's login.
type StaffMember struct {
	ID       int       `json:"id"`
	Name     string    `json:"name"`     // shown on screens and in the activity list
	Username string    `json:"username"` // lowercase
	Pass     string    `json:"pass"`     // "pbkdf2-sha256$rounds$salt$hash"
	Ver      int       `json:"ver"`      // goes up with a new password: old logins stop working
	Active   bool      `json:"active"`
	Created  time.Time `json:"created"`

	// At the shared till: the waiter's number ("Servitör 5") and a 4-digit
	// PIN, stored like the password (a slow fingerprint, never the PIN).
	No  int    `json:"no,omitempty"`
	PIN string `json:"pin,omitempty"`
}

// HasPIN reports whether the waiter can log in at the till with a PIN.
func (m StaffMember) HasPIN() bool { return m.PIN != "" }

// Activity is one thing staff did with money.
type Activity struct {
	At     time.Time `json:"at"`
	Actor  string    `json:"actor"` // "Anna", "Manager", "Head manager"
	Table  int       `json:"table,omitempty"`
	What   string    `json:"what"`   // e.g. "paid by card"
	Detail string    `json:"detail"` // e.g. "Guest 2 · 23,40 €"
}

// MaxActivity is how many activity lines are kept.
const MaxActivity = 5000

// pbkdf2SHA256 is PBKDF2 (RFC 8018) with HMAC-SHA256.
func pbkdf2SHA256(password, salt []byte, rounds, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hLen := prf.Size()
	blocks := (keyLen + hLen - 1) / hLen
	dk := make([]byte, 0, blocks*hLen)
	u := make([]byte, hLen)
	var ctr [4]byte
	for block := 1; block <= blocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(ctr[:], uint32(block))
		prf.Write(ctr[:])
		dk = prf.Sum(dk)
		t := dk[len(dk)-hLen:]
		copy(u, t)
		for n := 2; n <= rounds; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for i := range u {
				t[i] ^= u[i]
			}
		}
	}
	return dk[:keyLen]
}

func hashPassword(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	sum := pbkdf2SHA256([]byte(password), salt, pbkdf2Rounds, 32)
	return "pbkdf2-sha256$" + strconv.Itoa(pbkdf2Rounds) + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(sum)
}

func passwordMatches(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	rounds, err1 := strconv.Atoi(parts[1])
	salt, err2 := hex.DecodeString(parts[2])
	want, err3 := hex.DecodeString(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || rounds < 1 || rounds > 10_000_000 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, rounds, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked when a username doesn't exist, so a wrong username
// takes as long as a wrong password (timing doesn't tell which usernames exist).
var dummyHash = hashPassword("not a real password")

func cleanUsername(u string) string { return strings.ToLower(strings.TrimSpace(u)) }

// AddStaff adds a waiter.
func (s *Store) AddStaff(name, username, password string, now time.Time) (StaffMember, error) {
	name, username = strings.TrimSpace(name), cleanUsername(username)
	if name == "" || utf8.RuneCountInString(name) > 40 || username == "" || len(username) > 40 ||
		strings.ContainsAny(username, " \t") || utf8.RuneCountInString(password) < MinStaffPassword {
		return StaffMember{}, ErrBadStaff
	}
	hash := hashPassword(password) // slow: before taking the lock
	var out StaffMember
	err := s.changeOrders(func() (bool, error) {
		for _, m := range s.d.Staff {
			if m.Active && m.Username == username {
				return false, ErrUsernameTaken
			}
		}
		s.d.NextStaffID++
		m := &StaffMember{ID: s.d.NextStaffID, Name: name, Username: username, Pass: hash, Ver: 1, Active: true, Created: now, No: s.nextWaiterNo()}
		s.d.Staff = append(s.d.Staff, m)
		out = *m
		return true, nil
	})
	return out, err
}

// NewStaffPassword gives a waiter a new password (they're logged out everywhere).
func (s *Store) NewStaffPassword(id int, password string) error {
	if utf8.RuneCountInString(password) < MinStaffPassword {
		return ErrBadStaff
	}
	hash := hashPassword(password)
	return s.changeOrders(func() (bool, error) {
		for _, m := range s.d.Staff {
			if m.ID == id && m.Active {
				m.Pass, m.Ver = hash, m.Ver+1
				return true, nil
			}
		}
		return false, ErrNotFound
	})
}

// RemoveStaff removes a waiter's login (they're logged out everywhere).
// Their name stays in the activity list.
func (s *Store) RemoveStaff(id int) error {
	return s.changeOrders(func() (bool, error) {
		for _, m := range s.d.Staff {
			if m.ID == id && m.Active {
				m.Active, m.Pass, m.Ver = false, "", m.Ver+1
				return true, nil
			}
		}
		return false, ErrNotFound
	})
}

// StaffList lists the waiters who can log in, by name.
func (s *Store) StaffList() []StaffMember {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []StaffMember
	for _, m := range s.d.Staff {
		if m.Active {
			c := *m
			c.Pass = ""
			if c.PIN != "" {
				c.PIN = "set" // HasPIN, without handing out the fingerprint
			}
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// CheckStaffLogin checks a waiter's username and password.
func (s *Store) CheckStaffLogin(username, password string) (StaffMember, bool) {
	username = cleanUsername(username)
	s.mu.Lock()
	var found *StaffMember
	for _, m := range s.d.Staff {
		if m.Active && m.Username == username {
			c := *m
			found = &c
		}
	}
	s.mu.Unlock()
	if found == nil {
		passwordMatches(dummyHash, password) // same time as a real check
		return StaffMember{}, false
	}
	if !passwordMatches(found.Pass, password) {
		return StaffMember{}, false
	}
	found.Pass, found.PIN = "", ""
	return *found, true
}

// StaffSession checks that a waiter's login is still valid (not removed,
// no new password since), and returns them.
func (s *Store) StaffSession(id, ver int) (StaffMember, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.d.Staff {
		if m.ID == id && m.Active && m.Ver == ver {
			c := *m
			c.Pass, c.PIN = "", ""
			return c, true
		}
	}
	return StaffMember{}, false
}

// LogActivity records something staff did with money.
func (s *Store) LogActivity(a Activity) {
	_ = s.changeOrders(func() (bool, error) {
		s.d.Activity = append(s.d.Activity, &a)
		if over := len(s.d.Activity) - MaxActivity; over > 0 {
			s.d.Activity = append([]*Activity(nil), s.d.Activity[over:]...)
		}
		return true, nil
	})
}

// ActivityOn lists a day's activity, newest first.
func (s *Store) ActivityOn(date string) []Activity {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Activity
	for i := len(s.d.Activity) - 1; i >= 0; i-- {
		a := s.d.Activity[i]
		if schedule.FormatDate(a.At) == date {
			out = append(out, *a)
		}
	}
	return out
}

// ---------- the shared till: waiter number and PIN ----------

var ErrBadPIN = errors.New("a PIN is 4 to 6 digits")

// nextWaiterNo is the lowest waiter number not in use. Callers hold s.mu.
func (s *Store) nextWaiterNo() int {
	used := map[int]bool{}
	for _, m := range s.d.Staff {
		if m.Active {
			used[m.No] = true
		}
	}
	n := 1
	for used[n] {
		n++
	}
	return n
}

func validPIN(pin string) bool {
	if len(pin) < 4 || len(pin) > 6 {
		return false
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// SetStaffPIN gives a waiter a PIN for the till ("" removes it).
func (s *Store) SetStaffPIN(id int, pin string) error {
	hash := ""
	if pin != "" {
		if !validPIN(pin) {
			return ErrBadPIN
		}
		hash = hashPassword(pin)
	}
	return s.changeOrders(func() (bool, error) {
		for _, m := range s.d.Staff {
			if m.ID == id && m.Active {
				if m.No == 0 { // waiters added before numbers existed
					m.No = s.nextWaiterNo()
				}
				m.PIN = hash
				return true, nil
			}
		}
		return false, ErrNotFound
	})
}

// CheckStaffPIN checks a waiter number and PIN at the till. A wrong number
// takes as long as a wrong PIN.
func (s *Store) CheckStaffPIN(no int, pin string) (StaffMember, bool) {
	s.mu.Lock()
	var found *StaffMember
	for _, m := range s.d.Staff {
		if m.Active && m.No == no && m.PIN != "" {
			c := *m
			found = &c
		}
	}
	s.mu.Unlock()
	if found == nil || !validPIN(pin) {
		passwordMatches(dummyHash, pin)
		return StaffMember{}, false
	}
	if !passwordMatches(found.PIN, pin) {
		return StaffMember{}, false
	}
	found.Pass, found.PIN = "", ""
	return *found, true
}
