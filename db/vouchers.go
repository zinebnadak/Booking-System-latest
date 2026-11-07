package db

import (
	"crypto/rand"
	"errors"
	"math/big"
	"sort"
	"strings"
	"time"

	"emeal-booking/schedule"
)

// Gift cards (presentkort) and lunch coupon booklets (lunchkuponghäfte).
//
// Staff sell them on the Gift cards page: the guest pays at the terminal or
// in cash, and gets a printed card with a code. Later the code pays (part
// of) a bill: a gift card has money on it, a lunch booklet has coupons that
// each pay one lunch.

const (
	VoucherGift  = "gift"
	VoucherLunch = "lunch"
)

var (
	ErrNoVoucher   = errors.New("no gift card or lunch booklet with that code")
	ErrVoucherUsed = errors.New("nothing left on this card")
	ErrBadVoucher  = errors.New("amount or number of coupons not allowed")
	// ErrExtraCoupons: fewer coupons already cover what's left to pay.
	ErrExtraCoupons = errors.New("more coupons than needed")
)

// Most a gift card can hold, and most coupons in a booklet.
const (
	MaxGiftCents   = 100000 // 1000 €
	MaxLunchCoupon = 50
)

// Voucher is a gift card or a lunch booklet.
type Voucher struct {
	Code      string    `json:"code"` // e.g. "PK-7K4M-Q2PX"
	Kind      string    `json:"kind"` // VoucherGift or VoucherLunch
	SoldAt    time.Time `json:"sold_at"`
	SoldCents int       `json:"sold_cents"` // what the buyer paid
	SoldWith  string    `json:"sold_with"`  // how they paid (PaidCard, PaidCash)
	SoldBy    string    `json:"sold_by,omitempty"`
	Note      string    `json:"note,omitempty"` // e.g. who it's for

	// Gift card: its value, and what's left.
	Initial int `json:"initial,omitempty"`
	Cents   int `json:"cents,omitempty"`

	// Lunch booklet: coupons, coupons left, and what one coupon pays.
	Coupons     int `json:"coupons,omitempty"`
	Left        int `json:"left,omitempty"`
	CouponCents int `json:"coupon_cents,omitempty"`

	Uses []VoucherUse `json:"uses,omitempty"`
	Void bool         `json:"void,omitempty"` // sold by mistake and refunded
}

// VoucherUse is one time a code paid (part of) a bill.
type VoucherUse struct {
	At      time.Time `json:"at"`
	BillID  int       `json:"bill_id"`
	Cents   int       `json:"cents"`
	Coupons int       `json:"coupons,omitempty"`
}

// Value is what's left on it, in cents (for a booklet: coupons × lunch).
func (v Voucher) Value() int {
	if v.Kind == VoucherLunch {
		return v.Left * v.CouponCents
	}
	return v.Cents
}

// use works out what the voucher pays now (at most left), and takes it off.
func (v *Voucher) use(p Payment, left int) (cents, coupons int, err error) {
	switch v.Kind {
	case VoucherGift:
		if v.Cents <= 0 {
			return 0, 0, ErrVoucherUsed
		}
		cents = v.Cents
		if p.Cents > 0 {
			cents = min(cents, p.Cents)
		}
		cents = min(cents, left)
		v.Cents -= cents
		return cents, 0, nil
	case VoucherLunch:
		n := max(p.Coupons, 1)
		if v.Left <= 0 {
			return 0, 0, ErrVoucherUsed
		}
		if n > v.Left {
			return 0, 0, ErrBadVoucher
		}
		if (n-1)*v.CouponCents >= left {
			return 0, 0, ErrExtraCoupons // one coupon less would cover it: don't waste one
		}
		cents = min(n*v.CouponCents, left)
		v.Left -= n
		return cents, n, nil
	}
	return 0, 0, ErrNoVoucher
}

// giveBack undoes a use (the payment was taken back).
func (v *Voucher) giveBack(billID int, p Payment) {
	if v.Kind == VoucherGift {
		v.Cents = min(v.Cents+p.Cents, v.Initial)
	} else {
		v.Left = min(v.Left+p.Coupons, v.Coupons)
	}
	for i := len(v.Uses) - 1; i >= 0; i-- {
		if v.Uses[i].BillID == billID && v.Uses[i].Cents == p.Cents {
			v.Uses = append(v.Uses[:i], v.Uses[i+1:]...)
			break
		}
	}
}

// codeAlphabet has no 0/O, 1/I/L or 5/S, so codes read out loud or typed
// from paper don't get mixed up.
const codeAlphabet = "ABCDEFGHJKMNPQRTUVWXYZ2346789"

func newVoucherCode(kind string) string {
	var b strings.Builder
	if kind == VoucherLunch {
		b.WriteString("LH-")
	} else {
		b.WriteString("PK-")
	}
	for i := 0; i < 8; i++ {
		if i == 4 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		if err != nil {
			panic(err)
		}
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	return b.String()
}

// cleanCode makes "pk 7k4m q2px" into "PK-7K4M-Q2PX".
func cleanCode(code string) string {
	var raw []rune
	for _, r := range strings.ToUpper(code) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			raw = append(raw, r)
		}
	}
	if len(raw) != 10 {
		return ""
	}
	return string(raw[:2]) + "-" + string(raw[2:6]) + "-" + string(raw[6:])
}

// voucherByCode finds a voucher. Callers hold s.mu.
func (s *Store) voucherByCode(code string) *Voucher {
	c := cleanCode(code)
	if c == "" {
		return nil
	}
	for _, v := range s.d.Vouchers {
		if v.Code == c {
			return v
		}
	}
	return nil
}

// SellGiftCard makes a gift card worth cents, paid with method.
func (s *Store) SellGiftCard(cents int, method, by, note string, now time.Time) (Voucher, error) {
	if cents <= 0 || cents > MaxGiftCents {
		return Voucher{}, ErrBadVoucher
	}
	return s.sellVoucher(Voucher{Kind: VoucherGift, Initial: cents, Cents: cents, SoldCents: cents}, method, by, note, now)
}

// SellLunchBooklet makes a booklet of coupons, each paying one lunch of up
// to couponCents, sold for priceCents.
func (s *Store) SellLunchBooklet(coupons, couponCents, priceCents int, method, by, note string, now time.Time) (Voucher, error) {
	if coupons <= 0 || coupons > MaxLunchCoupon || couponCents <= 0 || couponCents > MaxGiftCents || priceCents <= 0 || priceCents > MaxGiftCents {
		return Voucher{}, ErrBadVoucher
	}
	return s.sellVoucher(Voucher{Kind: VoucherLunch, Coupons: coupons, Left: coupons, CouponCents: couponCents, SoldCents: priceCents}, method, by, note, now)
}

func (s *Store) sellVoucher(v Voucher, method, by, note string, now time.Time) (Voucher, error) {
	if method != PaidCash {
		method = PaidCard
	}
	v.SoldAt, v.SoldWith, v.SoldBy, v.Note = now, method, by, note
	err := s.changeOrders(func() (bool, error) {
		for {
			v.Code = newVoucherCode(v.Kind)
			if s.voucherByCode(v.Code) == nil {
				break
			}
		}
		c := v
		s.d.Vouchers = append(s.d.Vouchers, &c)
		return true, nil
	})
	return v, err
}

// VoucherByCode looks up a gift card or lunch booklet.
func (s *Store) VoucherByCode(code string) (Voucher, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.voucherByCode(code)
	if v == nil {
		return Voucher{}, false
	}
	return v.clone(), true
}

// VoidVoucher cancels a card sold by mistake (before it was used).
func (s *Store) VoidVoucher(code string) error {
	return s.changeOrders(func() (bool, error) {
		v := s.voucherByCode(code)
		if v == nil {
			return false, ErrNoVoucher
		}
		if len(v.Uses) > 0 || v.Void {
			return false, ErrVoucherUsed
		}
		v.Void = true
		return true, nil
	})
}

// Vouchers lists the newest gift cards and booklets first (at most n).
func (s *Store) Vouchers(n int) []Voucher {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Voucher
	for i := len(s.d.Vouchers) - 1; i >= 0 && len(out) < n; i-- {
		out = append(out, s.d.Vouchers[i].clone())
	}
	return out
}

// VouchersSoldOn lists what was sold on a day ("2026-10-30").
func (s *Store) VouchersSoldOn(date string) []Voucher {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Voucher
	for _, v := range s.d.Vouchers {
		if !v.Void && schedule.FormatDate(v.SoldAt) == date {
			out = append(out, v.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SoldAt.Before(out[j].SoldAt) })
	return out
}

func (v *Voucher) clone() Voucher {
	c := *v
	c.Uses = append([]VoucherUse(nil), v.Uses...)
	return c
}
