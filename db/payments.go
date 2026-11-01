package db

import (
	"errors"
	"sort"
	"time"
)

// Ways to pay, and paying a bill in parts ("Kvar att betala").
//
// A bill can be paid all at once (PayBill), guest by guest when it's split
// (PayShares), or in parts with any mix of methods (AddPayment): 50 € on a
// gift card, the rest at the terminal. The till shows what's left to pay
// until it's all paid, then the bill closes by itself.

// More ways to pay (PaidCard, PaidCash and PaidOnline are in orders.go and
// guests.go).
const (
	PaidEpassi      = "epassi"      // lunch benefit (ePassi)
	PaidSmartum     = "smartum"     // lunch benefit (Smartum)
	PaidEdenred     = "edenred"     // lunch benefit (Edenred)
	PaidGiftCard    = "giftcard"    // the restaurant's own gift card (presentkort)
	PaidLunchCoupon = "lunchcoupon" // a coupon from a lunch booklet (lunchkuponghäfte)
	PaidMixed       = "mixed"       // several of these
)

// StaffMethods are the ways staff can mark something paid with one tap, in
// the order the buttons show. Gift cards and lunch coupons need their code
// (AddPayment); online is only for guests' phones.
var StaffMethods = []string{PaidCard, PaidCash, PaidEpassi, PaidSmartum, PaidEdenred}

// AllMethods is every way a bill can be paid, in report order.
var AllMethods = []string{PaidCard, PaidCash, PaidOnline, PaidEpassi, PaidSmartum, PaidEdenred, PaidGiftCard, PaidLunchCoupon}

// staffMethod returns method if staff can tap it, otherwise the card terminal
// (the old behaviour for unknown values).
func staffMethod(method string) string {
	for _, m := range StaffMethods {
		if m == method {
			return m
		}
	}
	return PaidCard
}

// PrepaidMethod: paid with something bought earlier (a gift card or a lunch
// coupon). That money came in when it was sold, not now.
func PrepaidMethod(m string) bool { return m == PaidGiftCard || m == PaidLunchCoupon }

var (
	ErrPartlyPaid  = errors.New("part of this bill is already paid: pay the rest the same way")
	ErrNothingLeft = errors.New("nothing left to pay")
	ErrBadAmount   = errors.New("the amount must be more than 0 and at most what's left")
	ErrBadMethod   = errors.New("unknown way to pay")
)

// Payment is one part of a bill paid on its own.
type Payment struct {
	Method  string    `json:"method"`
	Cents   int       `json:"cents"`
	At      time.Time `json:"at"`
	Code    string    `json:"code,omitempty"`    // gift card or lunch booklet
	Coupons int       `json:"coupons,omitempty"` // lunch coupons used
}

// Paid is what part payments have covered so far.
func (b Bill) Paid() int {
	p := 0
	for _, x := range b.Payments {
		p += x.Cents
	}
	return p
}

// Left is what's still to pay on a bill paid in parts ("Kvar att betala").
func (b Bill) Left() int { return max(b.ToPay()-b.Paid(), 0) }

// Takings is what a bill brought in, per way of paying.
func (b Bill) Takings() map[string]int {
	out := map[string]int{}
	switch {
	case b.IsSplit():
		pay, _, _ := b.Parts()
		for i, sh := range b.Split {
			if len(sh.Payments) > 0 { // paid in parts: each part as it was paid
				for _, p := range sh.Payments {
					out[p.Method] += p.Cents
				}
				continue
			}
			if sh.PaidAt != nil && sh.PaidBy != PaidNothing {
				out[sh.PaidBy] += pay[i]
			}
		}
	case len(b.Payments) > 0:
		for _, p := range b.Payments {
			out[p.Method] += p.Cents
		}
	case b.PaidAt != nil:
		out[b.PaidBy] += b.ToPay()
	}
	return out
}

// closeWithPayments closes a bill whose part payments cover it.
func closeWithPayments(b *Bill, now time.Time) bool {
	if len(b.Payments) == 0 || b.Left() > 0 {
		return false
	}
	t := now
	b.PaidAt, b.PaidBy, b.QROpen = &t, methodOf(b.Payments), false
	return true
}

// methodOf is how parts were paid: their way, or PaidMixed if not all the same.
func methodOf(parts []Payment) string {
	if len(parts) == 0 {
		return ""
	}
	method := parts[0].Method
	for _, p := range parts {
		if p.Method != method {
			return PaidMixed
		}
	}
	return method
}

// takePayment checks a part payment of at most left on bill billID, and
// for a gift card or lunch coupon uses it up in the same step (so the same
// card can't pay twice). It fills in p. Callers hold s.mu, inside
// changeOrders.
func (s *Store) takePayment(billID int, p *Payment, left int, now time.Time) error {
	p.At = now
	switch p.Method {
	case PaidGiftCard, PaidLunchCoupon:
		v := s.voucherByCode(p.Code)
		if v == nil || v.Void || (p.Method == PaidGiftCard) != (v.Kind == VoucherGift) {
			return ErrNoVoucher
		}
		cents, coupons, err := v.use(*p, left)
		if err != nil {
			return err
		}
		p.Code, p.Cents, p.Coupons = v.Code, cents, coupons
		v.Uses = append(v.Uses, VoucherUse{At: now, BillID: billID, Cents: cents, Coupons: coupons})
	default:
		if staffMethod(p.Method) != p.Method {
			return ErrBadMethod
		}
		if p.Cents <= 0 || p.Cents > left {
			return ErrBadAmount
		}
		p.Code, p.Coupons = "", 0
	}
	return nil
}

// giveBackPayment: a part payment was taken back. A gift card or lunch
// booklet gets its money or coupons back. Callers hold s.mu.
func (s *Store) giveBackPayment(billID int, p Payment) {
	if v := s.voucherByCode(p.Code); v != nil {
		v.giveBack(billID, p)
	}
}

// AddPayment pays part of a bill (or the rest of it). Gift cards and lunch
// coupons are checked and used up in the same step, so the same card can't
// pay twice. For a lunch booklet, p.Coupons says how many coupons to use;
// each pays one lunch, up to what's left. The amount is never more than
// what's left. closed reports whether the bill is now fully paid.
func (s *Store) AddPayment(billID int, p Payment, now time.Time) (out Bill, closed bool, err error) {
	err = s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Lines) == 0 {
			return false, ErrEmptyBill
		}
		if b.IsSplit() {
			return false, ErrSplit
		}
		if busy := s.anyHold(b, now); busy != nil {
			return false, busy
		}
		left := b.Left()
		if left <= 0 {
			return false, ErrNothingLeft
		}
		if err := s.takePayment(b.ID, &p, left, now); err != nil {
			return false, err
		}
		b.Payments = append(b.Payments, p)
		closed = closeWithPayments(b, now)
		out = b.clone()
		return true, nil
	})
	return out, closed, err
}

// UndoPayment takes back the last part payment of an open bill (typed in by
// mistake). A gift card or lunch booklet gets its money or coupons back.
func (s *Store) UndoPayment(billID int, now time.Time) (Payment, error) {
	var undone Payment
	err := s.changeOrders(func() (bool, error) {
		b, err := s.openBillByID(billID)
		if err != nil {
			return false, err
		}
		if len(b.Payments) == 0 {
			return false, ErrNothingLeft
		}
		undone = b.Payments[len(b.Payments)-1]
		b.Payments = b.Payments[:len(b.Payments)-1]
		s.giveBackPayment(b.ID, undone)
		return true, nil
	})
	return undone, err
}

// Methods lists the ways of paying in a takings map, in report order.
func Methods(t map[string]int) []string {
	var out []string
	for _, m := range AllMethods {
		if t[m] != 0 {
			out = append(out, m)
		}
	}
	var other []string
	for m, c := range t {
		known := false
		for _, a := range AllMethods {
			known = known || a == m
		}
		if !known && c != 0 {
			other = append(other, m)
		}
	}
	sort.Strings(other)
	return append(out, other...)
}
