package db

import "time"

// Paying one guest's part of a split bill in parts: a gift card or a lunch
// coupon first, the rest at the terminal ("Kvar att betala", guest by guest).
//
// A guest who has paid some of their part:
//   - can't have their dishes changed or moved (like a paid guest),
//   - pays the rest on their own (not together with others, not on their
//     phone: the waiter is already with them),
//   - is paid when the parts cover their part (PaidBy is the way, or
//     PaidMixed).

// Started: this guest has paid, or paid some of their part.
func (sh Share) Started() bool { return sh.PaidAt != nil || len(sh.Payments) > 0 }

// PartPaid is what this guest's part payments have covered so far.
func (sh Share) PartPaid() int {
	n := 0
	for _, p := range sh.Payments {
		n += p.Cents
	}
	return n
}

// ShareLeft is what guest g still has to pay: their part, minus what they
// paid of it already. 0 once they've paid.
func (b Bill) ShareLeft(g int) int {
	sh := b.share(g)
	if sh == nil || sh.PaidAt != nil || g < 1 || g > len(b.Split) {
		return 0
	}
	pay, _, _ := b.Parts()
	return max(pay[g-1]-sh.PartPaid(), 0)
}

// AddSharePayment pays part of guest g's part (or the rest of it): a gift
// card, a lunch coupon, a lunch card, some in cash… Gift cards and coupons
// are checked and used up in the same step. When the parts cover what the
// guest owes, they've paid; when everyone has, the bill is paid (closed).
func (s *Store) AddSharePayment(billID, g int, p Payment, now time.Time) (out Bill, closed bool, err error) {
	err = s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if !b.IsSplit() {
			return false, ErrNotSplit
		}
		if len(b.Unassigned()) > 0 {
			return false, ErrUnassigned
		}
		sh := b.share(g)
		if sh == nil {
			return false, ErrBadGuest
		}
		if sh.PaidAt != nil {
			return false, ErrSharePaid
		}
		if busy := s.holdOn(b, []int{g}, nil, now); busy != nil {
			return false, busy // paying on their phone right now
		}
		if s.ordersWaiting(b.ID, []int{g}, false) {
			return false, ErrOrderPending
		}
		left := b.ShareLeft(g)
		if left <= 0 {
			return false, ErrNothingLeft
		}
		if err := s.takePayment(b.ID, &p, left, now); err != nil {
			return false, err
		}
		sh.Payments = append(sh.Payments, p)
		if b.ShareLeft(g) == 0 {
			t := now
			sh.PaidAt, sh.PaidBy, sh.Payer = &t, methodOf(sh.Payments), 0
			closed = closeIfDone(b, now)
		}
		out = b.clone()
		return true, nil
	})
	return out, closed, err
}

// UndoSharePart takes back the last part payment of a guest who hasn't
// paid all of their part yet (typed in by mistake). A gift card or lunch
// booklet gets it back.
func (s *Store) UndoSharePart(billID, g int) (Payment, error) {
	var undone Payment
	err := s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		sh := b.share(g)
		if sh == nil {
			return false, ErrBadGuest
		}
		if sh.PaidAt != nil {
			return false, ErrSharePaid // "Undo" on the paid guest does it
		}
		if len(sh.Payments) == 0 {
			return false, ErrNothingLeft
		}
		undone = sh.Payments[len(sh.Payments)-1]
		sh.Payments = sh.Payments[:len(sh.Payments)-1]
		s.giveBackPayment(b.ID, undone)
		return true, nil
	})
	return undone, err
}

// TableLeft is everything still to pay at a split table: the guests who
// haven't paid (minus what they paid of it already), and what nobody had.
func (b Bill) TableLeft() int {
	pay, _, _ := b.Parts()
	left := b.ToPay()
	for i, sh := range b.Split {
		if sh.PaidAt != nil {
			left -= pay[i]
		} else {
			left -= sh.PartPaid()
		}
	}
	return max(left, 0)
}

// PayTable: the waiter takes one payment for everything still to pay at a
// split table. That's every guest who hasn't paid, and what nobody had yet
// (for example what the waiter typed in for the table before guests joined
// with their phones). What nobody had goes on the first unpaid guest's part,
// or, if everyone else has paid, on a new guest: the table. A guest who paid
// some of their part already pays the rest on their own first.
func (s *Store) PayTable(billID int, method string, now time.Time) (cents int, err error) {
	err = s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if !b.IsSplit() {
			return false, ErrNotSplit
		}
		if len(b.Payments) > 0 {
			return false, ErrPartlyPaid
		}
		if busy := s.anyHold(b, now); busy != nil {
			return false, busy // a guest is paying on their phone right now
		}
		var who []int
		for _, sh := range b.Split {
			if sh.PaidAt != nil {
				continue
			}
			if len(sh.Payments) > 0 {
				return false, ErrPartlyPaid
			}
			who = append(who, sh.Guest)
		}
		if s.ordersWaiting(b.ID, who, true) {
			return false, ErrOrderPending
		}
		if un := b.Unassigned(); len(un) > 0 {
			if len(who) == 0 {
				if len(b.Split) >= MaxGuests {
					return false, ErrTableFull
				}
				g := len(b.Split) + 1
				b.Split = append(b.Split, Share{Guest: g, Ref: newRef()})
				who = []int{g}
			}
			for _, l := range un {
				if _, bl := b.line(l.ID); bl != nil {
					bl.Guests = []int{who[0]}
				}
			}
		}
		var paying []int
		for _, g := range who {
			if b.hadSomething(g) {
				paying = append(paying, g)
			}
		}
		if len(paying) == 0 {
			return false, ErrNothingToPay
		}
		method = staffMethod(method)
		pay, _, _ := b.Parts()
		t := now
		for _, g := range paying {
			sh := b.share(g)
			sh.PaidAt, sh.PaidBy, sh.Payer = &t, method, 0
			if len(paying) > 1 {
				sh.Payer = paying[0]
			}
			cents += pay[g-1]
		}
		closeIfDone(b, now)
		return true, nil
	})
	return cents, err
}
