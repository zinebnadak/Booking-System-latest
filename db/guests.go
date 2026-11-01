package db

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ozmo-booking/schedule"
)

// Guests ordering and paying with the QR code on their table.
//
// Staff open a table for QR orders. Each phone that scans the table's code
// becomes the next guest on the table's bill ("Guest 3"); what they order is
// theirs from the start, so the bill is split by itself. They can pay their
// own part, or the whole table, on their phone.
//
// Paying holds the parts it pays for: while one guest pays for the whole
// table, nobody else can pay any of it (and the other way round), so the
// same part can never be paid twice. A hold ends when the payment goes
// through, when the payer cancels, or after PayHoldFor; an unfinished card
// payment is cancelled at Stripe before its parts are let go.

var (
	ErrTableClosed  = errors.New("this table isn't open for QR orders")
	ErrTableFull    = errors.New("this table has the most guests a bill can have")
	ErrBusy         = errors.New("someone is paying for this right now")
	ErrNothingToPay = errors.New("there is nothing left to pay")
	ErrQRGuests     = errors.New("guests joined with the QR code: the bill stays split")
	ErrPaidOnline   = errors.New("paid online: give the money back in Stripe instead")
	ErrTooSmall     = errors.New("too small to pay by card online")
	ErrNotPaidYet   = errors.New("not paid yet")
	ErrTooManyMails = errors.New("enough receipts sent by email")
)

// MaxReceiptEmails is how many times one guest's receipt can be emailed.
const MaxReceiptEmails = 3

// MinOnlineCents is the smallest amount Stripe takes (50 cents).
const MinOnlineCents = 50

// PaidNothing: a guest who had nothing when the table was paid.
const PaidNothing = "nothing"

// PaidOnline: a guest paid on their phone (Stripe).
const PaidOnline = "online"

// PayHoldFor is how long a payment on a phone keeps the parts it pays for.
const PayHoldFor = 3 * time.Minute

// TableQR is the code printed on a table's QR sticker.
type TableQR struct {
	Table int    `json:"table"`
	Code  string `json:"code"`
}

// PayAttempt is a guest paying on their phone. While it's pending, the parts
// it pays for are held.
type PayAttempt struct {
	Ref           string    `json:"ref"` // "tab-…": also Stripe's client_reference_id
	BillID        int       `json:"bill_id"`
	Payer         int       `json:"payer"`           // the guest paying
	Guests        []int     `json:"guests"`          // parts it pays (the payer's included)
	Lines         []int     `json:"lines,omitempty"` // lines nobody had yet, paid too ("the whole table")
	All           bool      `json:"all,omitempty"`   // paying for the whole table
	Cents         int       `json:"cents"`
	Created       time.Time `json:"created"`
	Until         time.Time `json:"until"` // the hold ends here, unless it's paid first
	SessionID     string    `json:"session_id,omitempty"`
	URL           string    `json:"url,omitempty"` // the payment page, to go back to it
	State         string    `json:"state"`         // "pending", "paid" or "released"
	PaymentIntent string    `json:"payment_intent,omitempty"`
	Overpaid      int       `json:"overpaid,omitempty"`  // cents paid for parts someone else had paid meanwhile: refund
	TipCents      int       `json:"tip_cents,omitempty"` // a tip for the staff, on top of Cents
	TipPercent    int       `json:"tip_percent,omitempty"`

	// PaidAt: when the payment came through. Seen: a waiter tapped OK on
	// "Paid on phones" (so the screens stop telling them).
	PaidAt *time.Time `json:"paid_at,omitempty"`
	Seen   bool       `json:"seen,omitempty"`
}

// TipChoices are the tips a guest can add when paying on their phone (%).
var TipChoices = []int{0, 5, 10}

// TipFor is the tip on an amount: pct percent, rounded to the cent.
func TipFor(cents, pct int) int { return (cents*pct + 50) / 100 }

// Total is what the guest pays: their part(s) and the tip.
func (a PayAttempt) Total() int { return a.Cents + a.TipCents }

// Pending reports whether the attempt still holds its parts.
func (a PayAttempt) Pending() bool { return a.State == "pending" }

func (a *PayAttempt) clone() PayAttempt {
	c := *a
	c.Guests = append([]int(nil), a.Guests...)
	c.Lines = append([]int(nil), a.Lines...)
	if a.PaidAt != nil {
		t := *a.PaidAt
		c.PaidAt = &t
	}
	return c
}

// PaidOnlineToSee lists phone payments from the last 12 hours that no
// waiter has seen yet, oldest first, so the Orders screen can tell them
// "Table 5 paid on the phone".
func (s *Store) PaidOnlineToSee(now time.Time) []PayAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PayAttempt
	for _, a := range s.d.PayAttempts {
		if a.State == "paid" && !a.Seen && a.PaidAt != nil && now.Sub(*a.PaidAt) < 12*time.Hour {
			out = append(out, a.clone())
		}
	}
	return out
}

// SeePayment: a waiter tapped OK on a phone payment.
func (s *Store) SeePayment(ref string) error {
	return s.changeOrders(func() (bool, error) {
		a := s.attempt(ref)
		if a == nil || a.State != "paid" {
			return false, ErrNotFound
		}
		if a.Seen {
			return false, nil
		}
		a.Seen = true
		return true, nil
	})
}

func (a *PayAttempt) covers(guests, lines []int) bool {
	for _, g := range guests {
		for _, x := range a.Guests {
			if g == x {
				return true
			}
		}
	}
	for _, l := range lines {
		for _, x := range a.Lines {
			if l == x {
				return true
			}
		}
	}
	return false
}

// BusyError: someone is paying for these parts right now.
type BusyError struct {
	Payer int       // the guest paying
	Name  string    // their name, if they gave one
	All   bool      // they're paying for the whole table
	Until time.Time // when the hold ends if they don't finish
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("guest %d is paying until %s", e.Payer, e.Until.Format("15:04:05"))
}

// Is lets errors.Is(err, ErrBusy) work.
func (e *BusyError) Is(target error) bool { return target == ErrBusy }

// StaleHoldError: a hold ran out, but its card payment must be cancelled at
// Stripe before the parts are let go. Refs are the attempts to cancel.
type StaleHoldError struct{ Refs []string }

func (e *StaleHoldError) Error() string { return "a finished hold still has an open payment page" }

// holdOn returns who holds any of these guests' parts or lines, or nil.
// A hold whose time ran out without a Stripe page is let go here and now;
// one with a Stripe page keeps holding until that page is cancelled.
// Callers hold s.mu (and may change things).
func (s *Store) holdOn(b *Bill, guests, lines []int, now time.Time) *BusyError {
	for _, a := range s.d.PayAttempts {
		if a.BillID != b.ID || !a.Pending() {
			continue
		}
		if !now.Before(a.Until) && a.SessionID == "" {
			a.State = "released"
			continue
		}
		if a.covers(guests, lines) {
			e := &BusyError{Payer: a.Payer, All: a.All, Until: a.Until}
			if sh := b.share(a.Payer); sh != nil {
				e.Name = sh.Name
			}
			return e
		}
	}
	return nil
}

// anyHold reports whether some payment on the bill is in progress.
func (s *Store) anyHold(b *Bill, now time.Time) *BusyError {
	all := make([]int, 0, len(b.Split))
	for _, sh := range b.Split {
		all = append(all, sh.Guest)
	}
	var lines []int
	for _, l := range b.Lines {
		lines = append(lines, l.ID)
	}
	return s.holdOn(b, all, lines, now)
}

// QRGuests reports whether guests joined the bill with the table's QR code.
func (b Bill) QRGuests() bool {
	for _, sh := range b.Split {
		if sh.Key != "" {
			return true
		}
	}
	return false
}

// closeIfDone pays the bill once every part is paid and nothing is left that
// nobody had. The table is then free (and closed for QR orders).
//
// A guest who had nothing (scanned the code but ordered nothing, or was
// added by mistake) doesn't keep the table open.
func closeIfDone(b *Bill, now time.Time) bool {
	if len(b.Unassigned()) > 0 {
		return false
	}
	paid := 0
	for _, sh := range b.Split {
		if sh.PaidAt != nil {
			paid++
		} else if b.hadSomething(sh.Guest) {
			return false
		}
	}
	if paid == 0 {
		return false
	}
	t := now
	for i := range b.Split {
		if b.Split[i].PaidAt == nil {
			b.Split[i].PaidAt, b.Split[i].PaidBy = &t, PaidNothing
		}
	}
	b.PaidAt, b.PaidBy, b.QROpen = &t, PaidSplit, false
	return true
}

func (b *Bill) hadSomething(g int) bool {
	for _, l := range b.Lines {
		if l.Had(g) {
			return true
		}
	}
	return false
}

// ---------- table QR codes ----------

// ensureTableQRs gives every table a QR code. Callers hold s.mu.
func (s *Store) ensureTableQRs() bool {
	have := map[int]bool{}
	for _, q := range s.d.TableQRs {
		have[q.Table] = true
	}
	added := false
	for _, t := range schedule.TableNos() {
		if !have[t] {
			s.d.TableQRs = append(s.d.TableQRs, &TableQR{Table: t, Code: newRef()})
			added = true
		}
	}
	return added
}

// TableQRs lists every table's QR code, by table.
func (s *Store) TableQRs() []TableQR {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TableQR, 0, len(s.d.TableQRs))
	for _, t := range schedule.TableNos() {
		for _, q := range s.d.TableQRs {
			if q.Table == t {
				out = append(out, *q)
			}
		}
	}
	return out
}

// NewTableQR gives a table a new code (a photo of the old sticker got around):
// the old sticker stops working.
func (s *Store) NewTableQR(table int) (TableQR, error) {
	var out TableQR
	err := s.changeOrders(func() (bool, error) {
		for _, q := range s.d.TableQRs {
			if q.Table == table {
				q.Code = newRef()
				out = *q
				return true, nil
			}
		}
		return false, ErrNoTable
	})
	return out, err
}

// tableForQR finds the table with this code (comparing every code in full,
// so timing gives nothing away), 0 if none. Callers hold s.mu.
func (s *Store) tableForQR(code string) int {
	found := 0
	for _, q := range s.d.TableQRs {
		if len(code) == len(q.Code) && subtle.ConstantTimeCompare([]byte(code), []byte(q.Code)) == 1 {
			found = q.Table
		}
	}
	return found
}

// TableForQR is tableForQR for the handlers.
func (s *Store) TableForQR(code string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tableForQR(code)
}

// OpenForGuests opens a table for QR orders (opening an empty bill if it has
// none), or closes it again. bookingRef and deposit: the booking seated
// there, whose online deposit comes off the new bill.
func (s *Store) OpenForGuests(table int, open bool, bookingRef string, deposit int, now time.Time) (Bill, error) {
	var out Bill
	err := s.changeOrders(func() (bool, error) {
		if !validTable(table) {
			return false, ErrNoTable
		}
		b := s.openBillAt(table)
		if b == nil {
			if !open {
				return false, nil
			}
			b = s.newBill(table, bookingRef, deposit, now)
		}
		b.QROpen = open
		out = b.clone()
		if !open && len(b.Lines) == 0 && len(b.Split) == 0 {
			// Opened for QR orders but nobody came: no bill after all.
			for i, x := range s.d.Bills {
				if x == b {
					s.d.Bills = append(s.d.Bills[:i], s.d.Bills[i+1:]...)
					break
				}
			}
		}
		return true, nil
	})
	return out, err
}

// OpenOnArrival opens a table for QR orders when guests sit down (a booking
// marked as arrived, or walk-ins seated). A table whose bill already has
// orders on it is left alone: those are other guests who haven't paid yet,
// and the new ones must not end up on their bill. Reports whether it opened.
func (s *Store) OpenOnArrival(table int, bookingRef string, deposit int, now time.Time) (bool, error) {
	opened := false
	err := s.changeOrders(func() (bool, error) {
		if !validTable(table) {
			return false, ErrNoTable
		}
		b := s.openBillAt(table)
		switch {
		case b == nil:
			b = s.newBill(table, bookingRef, deposit, now)
		case len(b.Lines) > 0 || len(b.Split) > 0:
			return false, nil
		case b.BookingRef == "" && bookingRef != "" && deposit > 0 && !s.depositUsed(bookingRef):
			b.BookingRef, b.DepositCents = bookingRef, deposit
		}
		if !b.QROpen {
			b.QROpen = true
			opened = true
		}
		return true, nil
	})
	return opened, err
}

// TidyEmptyBills removes bills from earlier days that never got anything on
// them (a table opened for QR orders where nobody ordered).
func (s *Store) TidyEmptyBills(now time.Time) error {
	today := schedule.FormatDate(schedule.Today(now))
	return s.changeOrders(func() (bool, error) {
		kept := s.d.Bills[:0]
		removed := false
		for _, b := range s.d.Bills {
			if b.Open() && len(b.Lines) == 0 && schedule.FormatDate(b.OpenedAt) < today {
				removed = true
				continue
			}
			kept = append(kept, b)
		}
		s.d.Bills = kept
		return removed, nil
	})
}

// JoinTable: a phone scanned the table's QR code and becomes the next guest.
func (s *Store) JoinTable(code string, now time.Time) (Bill, Share, error) {
	var bill Bill
	var guest Share
	err := s.changeOrders(func() (bool, error) {
		table := s.tableForQR(code)
		if table == 0 {
			return false, ErrNoTable
		}
		b := s.openBillAt(table)
		if b == nil || !b.QROpen || len(b.Payments) > 0 {
			return false, ErrTableClosed // (part paid at the till: the rest goes there too)
		}
		if len(b.Split) >= MaxGuests {
			return false, ErrTableFull
		}
		guest = Share{Guest: len(b.Split) + 1, Ref: newRef(), Key: newRef()}
		b.Split = append(b.Split, guest)
		bill = b.clone()
		return true, nil
	})
	return bill, guest, err
}

// guestByKey finds a QR guest, on any bill. Callers hold s.mu.
func (s *Store) guestByKey(key string) (*Bill, *Share) {
	if len(key) != 32 {
		return nil, nil
	}
	for _, b := range s.d.Bills {
		for i := range b.Split {
			k := b.Split[i].Key
			if k != "" && subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 {
				return b, &b.Split[i]
			}
		}
	}
	return nil, nil
}

// GuestByKey returns a QR guest's bill and guest number.
func (s *Store) GuestByKey(key string) (Bill, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, sh := s.guestByKey(key)
	if b == nil {
		return Bill{}, 0, false
	}
	return b.clone(), sh.Guest, true
}

// StillAtTable reports whether a guest key (remembered by their phone) is a
// guest at this table's open bill: scanning again then brings them back to
// their own page instead of making a new guest.
func (s *Store) StillAtTable(code, key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	table := s.tableForQR(code)
	b, _ := s.guestByKey(key)
	return table != 0 && b != nil && b.Table == table && b.Open()
}

// SetGuestName lets a guest say who they are ("Anna"), so the waiter knows
// whose plate is whose.
func (s *Store) SetGuestName(key, name string) error {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > 30 {
		name = string([]rune(name)[:30])
	}
	return s.changeOrders(func() (bool, error) {
		b, sh := s.guestByKey(key)
		if b == nil || !b.Open() {
			return false, ErrNoBill
		}
		if sh.Name == name {
			return false, nil
		}
		sh.Name = name
		return true, nil
	})
}

// GuestOrder puts what a QR guest ordered on the table's bill, as theirs.
// Food goes to the chef, drinks to the waiters' "to bring" list. With
// "check phone orders" on, it waits for a waiter first (waiting reports that).
func (s *Store) GuestOrder(key string, lines []NewLine, now time.Time) (waiting bool, err error) {
	err = s.changeOrders(func() (bool, error) {
		b, sh := s.guestByKey(key)
		if b == nil || !b.Open() {
			return false, ErrBillClosed
		}
		if !b.QROpen {
			return false, ErrTableClosed
		}
		if len(lines) == 0 {
			return false, ErrNothingToAdd
		}
		if sh.PaidAt != nil {
			return false, ErrSharePaid
		}
		if busy := s.holdOn(b, []int{sh.Guest}, nil, now); busy != nil {
			return false, busy
		}
		// Counted now, also while a waiter checks it: whoever orders first
		// gets the last one. A rejected order gives it back.
		if err := s.takeStock(lines); err != nil {
			return false, err
		}
		if s.d.Settings.CheckPhoneOrders {
			s.d.NextPhoneOrderID++
			s.d.PhoneOrders = append(s.d.PhoneOrders, &PhoneOrder{ID: s.d.NextPhoneOrderID, BillID: b.ID, Table: b.Table,
				Guest: sh.Guest, Lines: append([]NewLine(nil), lines...), At: now, State: "pending"})
			waiting = true
			return true, nil
		}
		guest := *sh
		s.addLines(b, lines, &guest, true, now)
		return true, nil
	})
	return waiting, err
}

// ---------- paying on the phone ----------

// StartPayment holds what a QR guest is about to pay: their own part, or
// (all) every part nobody has paid yet plus anything nobody had. It all
// happens under one lock, so two phones can never hold the same part.
func (s *Store) StartPayment(key string, all bool, hold time.Duration, now time.Time) (PayAttempt, error) {
	return s.StartPaymentTip(key, all, 0, hold, now)
}

// StartPaymentTip is StartPayment with a tip of tipPct percent (one of
// TipChoices; anything else counts as no tip).
func (s *Store) StartPaymentTip(key string, all bool, tipPct int, hold time.Duration, now time.Time) (PayAttempt, error) {
	if !slices.Contains(TipChoices, tipPct) {
		tipPct = 0
	}
	var out PayAttempt
	err := s.changeOrders(func() (bool, error) {
		b, me := s.guestByKey(key)
		if b == nil || !b.Open() {
			return false, ErrBillClosed
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid
		}
		var guests, lines []int
		if all {
			for _, sh := range b.Split {
				if sh.PaidAt == nil {
					guests = append(guests, sh.Guest)
				}
			}
			for _, l := range b.Unassigned() {
				lines = append(lines, l.ID)
			}
		} else {
			if me.PaidAt != nil {
				return false, ErrSharePaid
			}
			guests = []int{me.Guest}
		}
		// An order still waiting for a waiter would change what they owe.
		if s.ordersWaiting(b.ID, guests, all) {
			return false, ErrOrderPending
		}
		// Someone paid some of their part with a waiter (a gift card): the
		// waiter takes the rest too.
		for _, g := range guests {
			if sh := b.share(g); sh != nil && len(sh.Payments) > 0 {
				return false, ErrPartlyPaid
			}
		}
		// A hold that ran out but still has a Stripe page open: that page
		// has to be cancelled first (the handler does it, then tries again).
		var stale []string
		for _, a := range s.d.PayAttempts {
			if a.BillID == b.ID && a.Pending() && !now.Before(a.Until) && a.SessionID != "" && a.covers(guests, lines) {
				stale = append(stale, a.Ref)
			}
		}
		if len(stale) > 0 {
			return false, &StaleHoldError{Refs: stale}
		}
		if busy := s.holdOn(b, guests, lines, now); busy != nil {
			return false, busy
		}
		pay, _, _ := b.Parts()
		cents := 0
		if all {
			paid := 0
			for i, sh := range b.Split {
				if sh.PaidAt != nil {
					paid += pay[i]
				}
			}
			cents = b.ToPay() - paid
		} else {
			cents = pay[me.Guest-1]
		}
		if len(guests) == 0 && len(lines) == 0 || cents <= 0 {
			return false, ErrNothingToPay
		}
		if cents < MinOnlineCents {
			return false, ErrTooSmall
		}
		a := &PayAttempt{
			Ref: "tab-" + newRef(), BillID: b.ID, Payer: me.Guest, Guests: guests, Lines: lines, All: all,
			Cents: cents, Created: now, Until: now.Add(hold), State: "pending",
			TipPercent: tipPct, TipCents: TipFor(cents, tipPct),
		}
		s.d.PayAttempts = append(s.d.PayAttempts, a)
		out = a.clone()
		return true, nil
	})
	return out, err
}

func (s *Store) attempt(ref string) *PayAttempt {
	for _, a := range s.d.PayAttempts {
		if a.Ref == ref {
			return a
		}
	}
	return nil
}

// AttemptByRef returns a payment attempt.
func (s *Store) AttemptByRef(ref string) (PayAttempt, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a := s.attempt(ref); a != nil {
		return a.clone(), true
	}
	return PayAttempt{}, false
}

// SetAttemptSession remembers the Stripe payment page of an attempt.
func (s *Store) SetAttemptSession(ref, sessionID, url string) error {
	return s.changeOrders(func() (bool, error) {
		a := s.attempt(ref)
		if a == nil {
			return false, ErrNotFound
		}
		a.SessionID, a.URL = sessionID, url
		return true, nil
	})
}

// AttemptPaid: the payment went through. Every part it held is paid (the
// payer is noted as having paid for the others), and the bill closes if
// nothing is left. Calling it again changes nothing.
func (s *Store) AttemptPaid(ref, paymentIntent string, now time.Time) (closed bool, err error) {
	err = s.changeOrders(func() (bool, error) {
		a := s.attempt(ref)
		if a == nil {
			return false, ErrNotFound
		}
		if a.State == "paid" {
			return false, nil
		}
		b := s.billByID(a.BillID)
		if b == nil {
			return false, ErrNoBill
		}
		a.State, a.PaymentIntent = "paid", paymentIntent
		paidAt := now
		a.PaidAt = &paidAt
		// What nobody had becomes the payer's.
		for _, id := range a.Lines {
			if _, l := b.line(id); l != nil && len(l.Guests) == 0 {
				l.Guests = []int{a.Payer}
			}
		}
		pay, _, _ := b.Parts()
		t := now
		for _, g := range a.Guests {
			sh := b.share(g)
			if sh == nil {
				continue
			}
			if sh.PaidAt != nil { // paid some other way meanwhile: should never happen, but never lose money
				a.Overpaid += pay[g-1]
				continue
			}
			if g != a.Payer && !b.hadSomething(g) { // held only so they couldn't order meanwhile
				continue
			}
			sh.PaidAt, sh.PaidBy = &t, PaidOnline
			if len(a.Guests) > 1 || a.All {
				sh.Payer = a.Payer
			}
		}
		if b.Open() {
			if sh := b.share(a.Payer); sh != nil {
				sh.TipCents += a.TipCents
			}
			closed = closeIfDone(b, now)
		} else {
			a.Overpaid = a.Total() // the bill was closed some other way: all of it back, tip too
		}
		return true, nil
	})
	return closed, err
}

// ReleaseAttempt lets go of a payment that won't happen (cancelled, or its
// Stripe page is expired).
func (s *Store) ReleaseAttempt(ref string) error {
	return s.changeOrders(func() (bool, error) {
		a := s.attempt(ref)
		if a == nil {
			return false, ErrNotFound
		}
		if !a.Pending() {
			return false, nil
		}
		a.State = "released"
		return true, nil
	})
}

// AttemptsToCancel lists holds that ran out while their Stripe page is still
// open: the page must be cancelled at Stripe, then the hold let go.
func (s *Store) AttemptsToCancel(now time.Time) []PayAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PayAttempt
	for _, a := range s.d.PayAttempts {
		if a.Pending() && !now.Before(a.Until) && a.SessionID != "" {
			out = append(out, a.clone())
		}
	}
	return out
}

// ReleaseExpired lets go of demo holds (no Stripe page) whose time ran out.
// It reports whether any were let go, so screens can be told.
func (s *Store) ReleaseExpired(now time.Time) bool {
	released := false
	_ = s.changeOrders(func() (bool, error) {
		for _, a := range s.d.PayAttempts {
			if a.Pending() && !now.Before(a.Until) && a.SessionID == "" {
				a.State = "released"
				released = true
			}
		}
		return released, nil
	})
	return released
}

// Holds lists the payments in progress on a bill.
func (s *Store) Holds(billID int, now time.Time) []PayAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PayAttempt
	for _, a := range s.d.PayAttempts {
		if a.BillID == billID && a.Pending() {
			out = append(out, a.clone())
		}
	}
	return out
}

// Overpayments lists payments that paid for something already paid, for a
// refund (should never happen thanks to the holds; shown to staff if it does).
func (s *Store) Overpayments(billID int) []PayAttempt {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PayAttempt
	for _, a := range s.d.PayAttempts {
		if a.BillID == billID && a.Overpaid > 0 {
			out = append(out, a.clone())
		}
	}
	return out
}

// ---------- staff adding for one guest ----------

// AddForGuest puts what staff typed in on the bill as one guest's (the
// guest asked the waiter instead of using their phone).
func (s *Store) AddForGuest(billID, g int, lines []NewLine, now time.Time) (Bill, *Ticket, error) {
	var out Bill
	var ticket *Ticket
	err := s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(lines) == 0 {
			return false, ErrNothingToAdd
		}
		sh := b.share(g)
		if sh == nil {
			return false, ErrBadGuest
		}
		if sh.PaidAt != nil {
			return false, ErrSharePaid
		}
		if busy := s.holdOn(b, []int{g}, nil, now); busy != nil {
			return false, busy
		}
		if err := s.takeStock(lines); err != nil {
			return false, err
		}
		guest := *sh
		ticket = s.addLines(b, lines, &guest, false, now)
		out = b.clone()
		return true, nil
	})
	return out, ticket, err
}

// ---------- receipts for guests who paid on their phone ----------

// paidShare finds a QR guest whose part is paid. Callers hold s.mu.
func (s *Store) paidShare(key string) (*Bill, *Share, error) {
	b, sh := s.guestByKey(key)
	if b == nil {
		return nil, nil, ErrNoBill
	}
	if sh.PaidAt == nil || sh.PaidBy == PaidNothing {
		return nil, nil, ErrNotPaidYet
	}
	return b, sh, nil
}

// AskPaperReceipt: the guest would like a printed receipt brought to them.
func (s *Store) AskPaperReceipt(key string, now time.Time) error {
	return s.changeOrders(func() (bool, error) {
		_, sh, err := s.paidShare(key)
		if err != nil {
			return false, err
		}
		if sh.PaperAt != nil && sh.PrintedAt == nil {
			return false, nil // already asked
		}
		t := now
		sh.PaperAt, sh.PrintedAt = &t, nil
		return true, nil
	})
}

// ReceiptGiven: a waiter brought the printed receipt.
func (s *Store) ReceiptGiven(billID, g int, now time.Time) error {
	return s.changeOrders(func() (bool, error) {
		b := s.billByID(billID)
		if b == nil {
			return false, ErrNoBill
		}
		sh := b.share(g)
		if sh == nil || sh.PaperAt == nil {
			return false, ErrBadGuest
		}
		if sh.PrintedAt != nil {
			return false, nil
		}
		t := now
		sh.PrintedAt = &t
		return true, nil
	})
}

// UseReceiptEmail counts one more emailed receipt for a guest who paid, and
// returns their bill and guest number. At most MaxReceiptEmails, so nobody
// can use the restaurant to send lots of email.
func (s *Store) UseReceiptEmail(key string) (Bill, int, error) {
	var out Bill
	var g int
	err := s.changeOrders(func() (bool, error) {
		b, sh, err := s.paidShare(key)
		if err != nil {
			return false, err
		}
		if sh.Emailed >= MaxReceiptEmails {
			return false, ErrTooManyMails
		}
		sh.Emailed++
		out, g = b.clone(), sh.Guest
		return true, nil
	})
	return out, g, err
}

// PaperRequest is a printed receipt a guest is waiting for.
type PaperRequest struct {
	BillID, Table, Guest int
	Name                 string
	At                   time.Time
}

// ReceiptsToPrint lists the printed receipts guests are waiting for, oldest first.
func (s *Store) ReceiptsToPrint() []PaperRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []PaperRequest
	for _, b := range s.d.Bills {
		for _, sh := range b.Split {
			if sh.PaperAt != nil && sh.PrintedAt == nil {
				out = append(out, PaperRequest{BillID: b.ID, Table: b.Table, Guest: sh.Guest, Name: sh.Name, At: *sh.PaperAt})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// ---------- calling from the phone ----------

// CallFromPhone: a guest taps "Call the waiter" (waiter) or, during the 3
// meals deal, "Ready for the next meal" on their phone. It works like the
// button on the table, and the staff see who called.
func (s *Store) CallFromPhone(key string, waiter bool, now time.Time) (PressResult, error) {
	var res PressResult
	err := s.change(func() (bool, error) {
		b, sh := s.guestByKey(key)
		if b == nil || !b.Open() {
			return false, ErrBillClosed
		}
		res.Table = b.Table
		run := s.activeRun(b.Table)
		if run == nil {
			waiter = true // no deal on this table: a call is a call
		}
		res.Kind = PressWaiter
		if !waiter {
			res.Meal = run.Meal()
			res.Kind = PressNext
			if run.LastMeal() {
				res.Kind = PressLast
			}
		}
		if _, c := s.callAt(b.Table); c != nil {
			res.Again = true
			if now.Sub(c.LastAt) >= PressGap {
				c.Presses++
				c.LastAt = now
			}
			c.Waiter = c.Waiter || waiter
			if c.Guest == 0 {
				c.Guest, c.Name = sh.Guest, sh.Name
			}
			return true, nil
		}
		if !waiter && !run.Waiting() {
			run.Ready = append(run.Ready, now)
		}
		s.d.Calls = append(s.d.Calls, &Call{Table: b.Table, At: now, LastAt: now, Presses: 1,
			Guest: sh.Guest, Name: sh.Name, Waiter: waiter})
		return true, nil
	})
	return res, err
}
