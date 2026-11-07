package db

import (
	"errors"
	"sort"
	"strings"
	"time"

	"emeal-booking/schedule"
)

// Nothing disappears from a bill without the manager.
//
// Once something is on a bill, a waiter can add more or take payment, but not
// take it off, lower the amount or the price, give a discount or void the
// bill. If something was added by mistake, the waiter asks the manager
// ("Ask the manager to take it off", with why); the manager takes it off or
// says no. Everything taken off, by anyone, is written down as a Removal,
// with what, how much, why, who asked and who did it, and the charts show it.
//
// If guests leave without paying (or the table is needed while the bill is
// still open), the waiter taps "Skip for now": the bill leaves the table,
// still open and unpaid, and waits for the manager. The table is free for
// the next guests. The manager can take the payment later, put the bill back
// on a table, or write it off as unpaid (a Removal too).

// RemoveAsk is a waiter asking the manager to take (some of) a line off.
type RemoveAsk struct {
	By  string    `json:"by"`
	At  time.Time `json:"at"`
	Why string    `json:"why"`
	Qty int       `json:"qty"` // how many of the line (all of it, or fewer)
}

// Removal is something taken off a bill.
type Removal struct {
	ID      int       `json:"id"`
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"` // RemovedLine, RemovedBill or RemovedUnpaid
	BillID  int       `json:"bill_id"`
	Table   int       `json:"table"`
	Name    string    `json:"name"`
	Qty     int       `json:"qty"`
	Cents   int       `json:"cents"` // what it was worth on the bill
	Why     string    `json:"why,omitempty"`
	AskedBy string    `json:"asked_by,omitempty"` // the waiter who asked
	By      string    `json:"by,omitempty"`       // who took it off
	Waiter  string    `json:"waiter,omitempty"`   // the table's waiter
}

// Kinds of removal.
const (
	RemovedLine   = "line"   // one dish or drink (or some of it)
	RemovedBill   = "bill"   // the whole bill voided
	RemovedUnpaid = "unpaid" // the guests left without paying, written off
	RemovedUndo   = "undo"   // the waiter undid a mis-tap right after adding
)

// UndoWindow is how long after adding a waiter can still undo a line
// themselves (if the chef hasn't finished it).
const UndoWindow = time.Minute

var (
	ErrNoReason  = errors.New("say why it should come off")
	ErrBadQty    = errors.New("that many isn't on the bill")
	ErrNoAsk     = errors.New("nobody asked to take this off")
	ErrSkipped   = errors.New("this bill was skipped: the manager decides")
	ErrNotSkip   = errors.New("this bill wasn't skipped")
	ErrTableBusy = errors.New("that table has an open bill")
	ErrTooLate   = errors.New("too late to undo: ask the manager")
)

// maxReason is how long a reason can be.
const maxReason = 200

func cleanReason(why string) string {
	why = strings.Join(strings.Fields(why), " ")
	if r := []rune(why); len(r) > maxReason {
		why = string(r[:maxReason])
	}
	return why
}

// recordRemoval writes one down. Callers hold s.mu.
func (s *Store) recordRemoval(b *Bill, kind, name string, qty, cents int, why, askedBy, by string, now time.Time) {
	s.d.NextRemovalID++
	s.d.Removals = append(s.d.Removals, &Removal{ID: s.d.NextRemovalID, At: now, Kind: kind, BillID: b.ID, Table: b.Table,
		Name: name, Qty: qty, Cents: cents, Why: why, AskedBy: askedBy, By: by, Waiter: b.WaiterName})
}

// AskRemove: a waiter asks the manager to take qty of a line off the bill.
func (s *Store) AskRemove(billID, lineID, qty int, why, by string, now time.Time) error {
	why = cleanReason(why)
	if why == "" {
		return ErrNoReason
	}
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		_, l := b.line(lineID)
		if l == nil {
			return false, ErrNoLine
		}
		if qty < 1 || qty > l.Qty {
			return false, ErrBadQty
		}
		l.Ask = &RemoveAsk{By: by, At: now, Why: why, Qty: qty}
		return true, nil
	})
}

// DropAsk: the manager says no (or the waiter takes the question back).
func (s *Store) DropAsk(billID, lineID int) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		_, l := b.line(lineID)
		if l == nil {
			return false, ErrNoLine
		}
		if l.Ask == nil {
			return false, nil
		}
		l.Ask = nil
		return true, nil
	})
}

// TakeOff takes qty of a line off the bill (all of it if qty is 0 or more
// than there is), and writes it down. The chef sees what he hasn't cooked
// yet struck through; stock comes back for what wasn't made. A waiter's
// question on the line is answered by it. kitchen reports whether the chef
// had it.
func (s *Store) TakeOff(billID, lineID, qty int, why, by string, now time.Time) (kitchen bool, err error) {
	return s.takeOff(billID, lineID, qty, RemovedLine, why, "", by, now)
}

// TakeOffAsked is TakeOff with the manager standing at the waiter's device
// (the manager's PIN): askedBy is the waiter.
func (s *Store) TakeOffAsked(billID, lineID, qty int, why, askedBy, by string, now time.Time) error {
	_, err := s.takeOff(billID, lineID, qty, RemovedLine, why, askedBy, by, now)
	return err
}

// UndoLine: the waiter takes back a line they just added (a mis-tap),
// within UndoWindow and before the chef finished it. Written down as such.
func (s *Store) UndoLine(billID, lineID int, by string, now time.Time) error {
	_, err := s.takeOff(billID, lineID, 0, RemovedUndo, "", "", by, now)
	return err
}

func (s *Store) takeOff(billID, lineID, qty int, kind, why, askedBy, by string, now time.Time) (kitchen bool, err error) {
	why = cleanReason(why)
	err = s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		i, l := b.line(lineID)
		if l == nil {
			return false, ErrNoLine
		}
		if b.touchesPaid(l.Guests) {
			return false, ErrSharePaid
		}
		if busy := s.holdOn(b, l.Guests, []int{l.ID}, s.clock()); busy != nil {
			return false, busy
		}
		if qty <= 0 || qty > l.Qty {
			qty = l.Qty
		}
		if l.Ask != nil {
			if askedBy == "" {
				askedBy = l.Ask.By
			}
			if why == "" {
				why = l.Ask.Why
			}
		}
		worth := l.Total() * qty / l.Qty
		made := false // cooked, or brought to the table: it's used up
		var t *Ticket
		if l.TicketID != 0 {
			if t = s.ticketByID(l.TicketID); t != nil {
				made = t.DoneAt != nil
			}
		}
		if kind == RemovedUndo && (made || now.Sub(l.AddedAt) > UndoWindow || now.Before(l.AddedAt)) {
			return false, ErrTooLate
		}
		if !made {
			s.giveStock(l.MenuItemID, qty)
		}
		s.recordRemoval(b, kind, l.Name, qty, worth, why, askedBy, by, now)
		if qty == l.Qty {
			if t != nil {
				for j := range t.Items {
					if t.Items[j].LineID == l.ID {
						t.Items[j].Cancelled = true
						kitchen = true
					}
				}
			}
			b.Lines = append(b.Lines[:i], b.Lines[i+1:]...)
		} else {
			if t != nil && !made {
				for j := range t.Items {
					if t.Items[j].LineID == l.ID {
						t.Items[j].Qty = l.Qty - qty
						kitchen = true
					}
				}
			}
			l.Qty -= qty
			l.Ask = nil
		}
		b.BilledAt = nil // the shown bill is out of date
		return true, nil
	})
	return kitchen, err
}

// SkipBill: the guests left without paying, or the table is needed. The
// bill leaves the table (still open and unpaid) and waits for the manager.
// An empty bill just goes.
func (s *Store) SkipBill(billID int, by string, now time.Time) error {
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if b.SkippedAt != nil {
			return false, nil
		}
		if len(b.Lines) == 0 && len(b.Payments) == 0 {
			b.Voided = true // nothing on it: nothing to keep
			return true, nil
		}
		t := now
		b.SkippedAt, b.SkippedBy, b.QROpen = &t, by, false
		return true, nil
	})
}

// SkippedBills are the bills skipped and still unpaid, oldest first.
func (s *Store) SkippedBills() []Bill {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Bill
	for _, b := range s.d.Bills {
		if b.Open() && b.SkippedAt != nil {
			out = append(out, b.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SkippedAt.Before(*out[j].SkippedAt) })
	return out
}

// BackToTable puts a skipped bill back on a table that is free.
func (s *Store) BackToTable(billID, table int) error {
	return s.changeOrders(func() (bool, error) {
		if !validTable(table) {
			return false, ErrNoTable
		}
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if b.SkippedAt == nil {
			return false, ErrNotSkip
		}
		if s.openBillAt(table) != nil {
			return false, ErrTableBusy
		}
		for _, t := range s.d.Tickets {
			if t.BillID == b.ID {
				t.Table = table
			}
		}
		b.Table, b.SkippedAt, b.SkippedBy = table, nil, ""
		return true, nil
	})
}

// VoidBillBy throws a whole bill away and writes down everything that was on
// it: kind RemovedBill (opened by mistake) or RemovedUnpaid (the guests
// never paid). Anything the chef still has from it is struck through.
func (s *Store) VoidBillBy(billID int, kind, why, by string, now time.Time) error {
	why = cleanReason(why)
	return s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid // take the part payments back first
		}
		if b.AnySharePaid() {
			return false, ErrSharePaid
		}
		if busy := s.anyHold(b, s.clock()); busy != nil {
			return false, busy
		}
		b.Voided = true
		for _, l := range b.Lines {
			asked := ""
			if l.Ask != nil {
				asked = l.Ask.By
			}
			s.recordRemoval(b, kind, l.Name, l.Qty, l.Total(), why, asked, by, now)
		}
		for _, t := range s.d.Tickets {
			if t.BillID == b.ID && t.DoneAt == nil {
				for j := range t.Items {
					t.Items[j].Cancelled = true
				}
			}
		}
		return true, nil
	})
}

// Removals lists what was taken off bills from one date to another (both
// included), newest first.
func (s *Store) Removals(from, to string) []Removal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Removal
	for _, r := range s.d.Removals {
		if d := schedule.FormatDate(r.At); d >= from && d <= to {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}

// AskedLine is a waiter's question waiting for the manager.
type AskedLine struct {
	Bill Bill
	Line BillLine
}

// Asks lists the lines waiters asked to take off, oldest first.
func (s *Store) Asks() []AskedLine {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AskedLine
	for _, b := range s.d.Bills {
		if !b.Open() {
			continue
		}
		for _, l := range b.Lines {
			if l.Ask != nil {
				out = append(out, AskedLine{Bill: b.clone(), Line: l})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Line.Ask.At.Before(out[j].Line.Ask.At) })
	return out
}

// SetManagerPIN sets the manager's PIN (4 to 6 digits; "" removes it).
func (s *Store) SetManagerPIN(pin string) error {
	hash := ""
	if pin != "" {
		if !validPIN(pin) {
			return ErrBadPIN
		}
		hash = hashPassword(pin)
	}
	return s.changeOrders(func() (bool, error) {
		s.d.Settings.ManagerPIN = hash
		return true, nil
	})
}

// HasManagerPIN reports whether the manager has set a PIN.
func (s *Store) HasManagerPIN() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Settings.ManagerPIN != ""
}

// CheckManagerPIN checks the manager's PIN (as slow as a password check).
func (s *Store) CheckManagerPIN(pin string) bool {
	s.mu.Lock()
	hash := s.d.Settings.ManagerPIN
	s.mu.Unlock()
	if hash == "" || !validPIN(pin) {
		passwordMatches(dummyHash, pin)
		return false
	}
	return passwordMatches(hash, pin)
}

// WaitingForManager counts what waits for the manager: waiters' questions
// and skipped bills.
func (s *Store) WaitingForManager() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, b := range s.d.Bills {
		if !b.Open() {
			continue
		}
		if b.SkippedAt != nil {
			n++
		}
		for _, l := range b.Lines {
			if l.Ask != nil {
				n++
			}
		}
	}
	return n
}
