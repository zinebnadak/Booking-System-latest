package db

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"
)

// A busy evening of random waiter actions, many of them wrong or out of
// order: orders, stock counts, split bills, gift cards and lunch coupons on
// one guest's part, payments taken back, voids, the kitchen finishing food.
// After every single action the books must still add up:
//
//   - a paid bill was paid exactly what it costs, and an open one never more;
//   - each guest's part payments never exceed their part, and a paid guest
//     paid exactly their part;
//   - a gift card's value is what's left plus what it paid, and every use of
//     a card is a payment on a bill (nothing lost when payments are undone);
//   - counted stock never goes below 0 and matches what's been ordered;
//   - a voided bill has no money on it.
//
// Then the store is reopened from disk and must look the same.
//
// EVENINGS=400 go test ./db -run RandomEvenings runs more of them.
func TestRandomEveningsKeepTheBooks(t *testing.T) {
	n := int64(40)
	if v, err := strconv.Atoi(os.Getenv("EVENINGS")); err == nil && v > 0 {
		n = int64(v)
	}
	for seed := int64(1); seed <= n; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) { randomEvening(t, seed, 500) })
	}
}

type evening struct {
	t       *testing.T
	s       *Store
	r       *rand.Rand
	now     time.Time
	menu    []MenuItem
	codes   []string
	counted map[int]bool // stock counted for these menu items
	base    map[int]int  // what was counted last
	usedAt  map[int]int  // how many were used when it was counted
	gone    map[int]int  // used for good: made or brought, then taken off a bill

	// One ingredient (cheese) used by two dishes.
	ingID     int
	ingUses   map[int]int // dish → grams per portion
	ingBase   int         // what was counted last
	ingUsedAt map[int]int // dishes used when it was counted
}

func randomEvening(t *testing.T, seed int64, steps int) {
	s := newStore(t)
	e := &evening{t: t, s: s, r: rand.New(rand.NewSource(seed)), now: at("17:00"),
		counted: map[int]bool{}, base: map[int]int{}, usedAt: map[int]int{}, gone: map[int]int{}}
	s.Clock = func() time.Time { return e.now }
	e.menu = s.Menu(true)
	for i := 0; i < 3; i++ {
		v, _ := s.SellGiftCard([]int{2000, 5000, 10000}[i], PaidCard, "test", "", e.now)
		e.codes = append(e.codes, v.Code)
	}
	for i := 0; i < 2; i++ {
		v, _ := s.SellLunchBooklet(5, 1200, 5400, PaidCash, "test", "", e.now)
		e.codes = append(e.codes, v.Code)
	}
	for _, m := range e.menu[:4] { // count a few items
		e.count(m.ID, 6)
	}
	cheese, _ := s.AddIngredient("Ost", "g", 40, 0)
	e.ingID, e.ingUses, e.ingUsedAt = cheese.ID, map[int]int{}, map[int]int{}
	for k, m := range e.menu[4:6] {
		m.Uses = []IngredientUse{{ID: cheese.ID, Amount: 3 + 2*k}}
		s.SaveMenuItem(m)
		e.ingUses[m.ID] = 3 + 2*k
	}
	e.countIngredient(40)
	for step := 0; step < steps; step++ {
		e.now = e.now.Add(time.Minute)
		what := e.act()
		e.check(fmt.Sprintf("seed %d, step %d (%s)", seed, step, what))
	}
	// Everything survives a restart.
	again, err := Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	for table := 1; table <= 6; table++ {
		a, b := s.BillsForTable(table), again.BillsForTable(table)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Fatalf("seed %d: table %d's bills differ after reopening", seed, table)
		}
	}
	if fmt.Sprint(s.Vouchers(100)) != fmt.Sprint(again.Vouchers(100)) || fmt.Sprint(s.Menu(false)) != fmt.Sprint(again.Menu(false)) {
		t.Fatalf("seed %d: cards or stock differ after reopening", seed)
	}
}

func (e *evening) pick(n int) int { return e.r.Intn(n) }

func (e *evening) countIngredient(n int) {
	if _, err := e.s.SetIngredient(e.ingID, n, 0); err == nil {
		e.ingBase = n
		for id := range e.ingUses {
			e.ingUsedAt[id] = e.used(id)
		}
	}
}

func (e *evening) count(id, n int) {
	if _, err := e.s.SetStock(id, n); err == nil {
		e.counted[id], e.base[id], e.usedAt[id] = n != NotCounted, n, e.used(id)
	}
}

// used is how many of an item have ever been ordered and not given back.
func (e *evening) used(id int) int {
	n := e.gone[id]
	for table := 1; table <= 6; table++ {
		for _, b := range e.s.BillsForTable(table) {
			for _, l := range b.Lines {
				if l.MenuItemID == id {
					n += l.Qty
				}
			}
		}
	}
	return n
}

// anyBill is a random bill on a random table (open or not), or nil.
func (e *evening) openBill() *Bill {
	var open []Bill
	for _, b := range e.s.OpenBills() {
		open = append(open, b)
	}
	if len(open) == 0 {
		return nil
	}
	b := open[e.pick(len(open))]
	return &b
}

func (e *evening) randomLines() []NewLine {
	var lines []NewLine
	for i := 0; i <= e.pick(3); i++ {
		m := e.menu[e.pick(len(e.menu))]
		lines = append(lines, NewLine{MenuItemID: m.ID, Name: m.Name, PriceCents: m.PriceCents, ListCents: m.PriceCents,
			Qty: 1 + e.pick(3), Drink: m.Drink, VAT: m.VAT(), Course: m.Course, Hold: e.pick(2) == 0})
	}
	return lines
}

func (e *evening) payment(left int) Payment {
	code := e.codes[e.pick(len(e.codes))]
	switch e.pick(4) {
	case 0:
		return Payment{Method: PaidGiftCard, Code: code, Cents: e.pick(2) * (1 + e.pick(max(left, 1)))}
	case 1:
		return Payment{Method: PaidLunchCoupon, Code: code, Coupons: 1 + e.pick(3)}
	case 2:
		return Payment{Method: PaidCash, Cents: 1 + e.pick(max(left, 1)+50)} // sometimes too much
	}
	return Payment{Method: PaidEpassi, Cents: 1 + e.pick(max(left, 1))}
}

// act does one random thing, and says what.
func (e *evening) act() string {
	s := e.s
	b := e.openBill()
	op := e.pick(18)
	if b != nil && b.IsSplit() && e.pick(2) == 0 {
		op = []int{7, 8, 9, 9, 9, 10, 11, 11, 12, 17}[e.pick(10)] // a split table: mostly paying
	}
	switch {
	case op < 4 || b == nil:
		table := 1 + e.pick(6)
		expect := 0
		if ob, ok := s.OpenBill(table); ok && e.pick(5) > 0 {
			expect = ob.ID
		}
		s.AddToBill(table, expect, e.randomLines(), "", 0, e.now)
		return "order"
	case op == 4 && len(b.Lines) > 0:
		l := b.Lines[e.pick(len(b.Lines))]
		made := false
		if tk, ok := s.TicketsForBill(b.ID)[l.TicketID]; ok && tk.DoneAt != nil {
			made = true
		}
		if _, err := s.RemoveLine(b.ID, l.ID); err == nil && made {
			e.gone[l.MenuItemID] += l.Qty
		}
		return "remove a line"
	case op == 5 && len(b.Lines) > 0:
		l := b.Lines[e.pick(len(b.Lines))]
		s.UpdateLine(b.ID, l.ID, l.PriceCents, 1+e.pick(4), l.Note)
		return "change a quantity"
	case op == 6:
		s.SplitBill(b.ID, 1+e.pick(4))
		return "split"
	case op == 7 && b.IsSplit() && len(b.Lines) > 0:
		l := b.Lines[e.pick(len(b.Lines))]
		gs := []int{1 + e.pick(len(b.Split))}
		if e.pick(3) == 0 {
			gs = append(gs, 1+e.pick(len(b.Split)))
		}
		s.AssignLine(b.ID, l.ID, gs)
		return "give a line to guests"
	case op == 8:
		s.SplitEvenly(b.ID)
		return "split evenly"
	case op == 9 && b.IsSplit():
		g := 1 + e.pick(len(b.Split))
		s.AddSharePayment(b.ID, g, e.payment(b.ShareLeft(g)), e.now)
		return "part of a guest's part"
	case op == 10 && b.IsSplit():
		s.UndoSharePart(b.ID, 1+e.pick(len(b.Split)))
		return "take back a guest's part payment"
	case op == 11 && b.IsSplit():
		gs := []int{1 + e.pick(len(b.Split))}
		if e.pick(3) == 0 {
			gs = append(gs, 1+e.pick(len(b.Split)))
		}
		s.PayShares(b.ID, gs, []string{PaidCard, PaidCash, PaidSmartum}[e.pick(3)], e.now)
		return "guests pay"
	case op == 12 && b.IsSplit():
		s.UndoShare(b.ID, 1+e.pick(len(b.Split)))
		return "undo a guest's payment"
	case op == 13:
		s.AddPayment(b.ID, e.payment(b.Left()), e.now)
		return "part of the bill"
	case op == 14:
		s.UndoPayment(b.ID, e.now)
		return "take back a part of the bill"
	case op == 15:
		s.PayBill(b.ID, []string{PaidCard, PaidCash, PaidEdenred}[e.pick(3)], e.now)
		return "pay the bill"
	case op == 16:
		if e.pick(6) == 0 {
			s.VoidBill(b.ID)
			return "void"
		}
		for _, tk := range s.TicketsForBill(b.ID) {
			switch e.pick(4) {
			case 0:
				s.TicketServed(tk.ID, false, e.now)
				return "served"
			case 1:
				s.TicketDone(tk.ID, true, e.now)
				return "kitchen undo"
			}
			s.TicketDone(tk.ID, false, e.now)
			break
		}
		return "kitchen done"
	case op == 17 && b.IsSplit():
		s.PayTable(b.ID, []string{PaidCard, PaidCash}[e.pick(2)], e.now)
		return "pay the whole table"
	default:
		if e.pick(3) == 0 {
			e.countIngredient([]int{0, 5, 12, 40}[e.pick(4)])
			return "count the cheese"
		}
		m := e.menu[e.pick(len(e.menu))]
		n := []int{NotCounted, 0, 1, 3, 8}[e.pick(5)]
		e.count(m.ID, n)
		return "count stock"
	}
}

func (e *evening) check(where string) {
	t, s := e.t, e.s
	t.Helper()
	paidByCode := map[string]int{}
	couponsByCode := map[string]int{}
	note := func(p Payment) {
		if p.Code != "" {
			paidByCode[p.Code] += p.Cents
			couponsByCode[p.Code] += p.Coupons
		}
	}
	for table := 1; table <= 6; table++ {
		for _, b := range s.BillsForTable(table) {
			for _, p := range b.Payments {
				note(p)
				if p.Cents <= 0 {
					t.Fatalf("%s: bill %d has a payment of %d", where, b.ID, p.Cents)
				}
			}
			pay, _, _ := b.Parts()
			for i, sh := range b.Split {
				for _, p := range sh.Payments {
					note(p)
					if p.Cents <= 0 {
						t.Fatalf("%s: bill %d guest %d has a payment of %d", where, b.ID, sh.Guest, p.Cents)
					}
				}
				if sh.PartPaid() > pay[i] {
					t.Fatalf("%s: bill %d guest %d paid %d of %d", where, b.ID, sh.Guest, sh.PartPaid(), pay[i])
				}
				if sh.PaidAt != nil && len(sh.Payments) > 0 && sh.PartPaid() != pay[i] {
					t.Fatalf("%s: bill %d guest %d is paid with parts %d, part is %d", where, b.ID, sh.Guest, sh.PartPaid(), pay[i])
				}
			}
			if b.Paid() > b.ToPay() {
				t.Fatalf("%s: bill %d paid %d of %d", where, b.ID, b.Paid(), b.ToPay())
			}
			for _, tk := range s.TicketsForBill(b.ID) {
				if tk.ServedAt != nil && tk.DoneAt == nil {
					t.Fatalf("%s: ticket %d served but not cooked", where, tk.ID)
				}
			}
			if b.Voided && (len(b.Payments) > 0 || b.AnySharePaid()) {
				t.Fatalf("%s: voided bill %d has money on it", where, b.ID)
			}
			if b.PaidAt != nil && !b.Voided {
				sum := 0
				for m, c := range b.Takings() {
					if c < 0 {
						t.Fatalf("%s: bill %d takings %s %d", where, b.ID, m, c)
					}
					sum += c
				}
				if sum != b.ToPay() {
					t.Fatalf("%s: paid bill %d took %d, costs %d (%v)", where, b.ID, sum, b.ToPay(), b.Takings())
				}
				if b.IsSplit() {
					for i, sh := range b.Split {
						if sh.PaidAt == nil || (sh.PaidBy == PaidNothing && pay[i] != 0) {
							t.Fatalf("%s: paid bill %d, guest %d: %+v owes %d", where, b.ID, sh.Guest, sh, pay[i])
						}
					}
				}
			}
		}
	}
	for _, tk := range s.ReadyToServe(e.now) {
		if b, err := s.BillByID(tk.BillID); err != nil || !b.Open() || tk.ServedAt != nil || tk.DoneAt == nil || tk.Bar {
			t.Fatalf("%s: ticket %d shouldn't wait to be picked up", where, tk.ID)
		}
	}
	for _, v := range s.Vouchers(100) {
		used, coupons := 0, 0
		for _, u := range v.Uses {
			used += u.Cents
			coupons += u.Coupons
		}
		if used != paidByCode[v.Code] || coupons != couponsByCode[v.Code] {
			t.Fatalf("%s: card %s says it paid %d (%d coupons), the bills say %d (%d)", where, v.Code, used, coupons, paidByCode[v.Code], couponsByCode[v.Code])
		}
		switch v.Kind {
		case VoucherGift:
			if v.Cents < 0 || v.Cents+used != v.Initial {
				t.Fatalf("%s: gift card %s: %d left + %d used != %d", where, v.Code, v.Cents, used, v.Initial)
			}
		case VoucherLunch:
			if v.Left < 0 || v.Left+coupons != v.Coupons {
				t.Fatalf("%s: booklet %s: %d left + %d used != %d", where, v.Code, v.Left, coupons, v.Coupons)
			}
		}
	}
	for _, ing := range s.Ingredients() {
		want := e.ingBase
		for id, amt := range e.ingUses {
			want -= amt * (e.used(id) - e.ingUsedAt[id])
		}
		if ing.Left < 0 || ing.Left != want {
			t.Fatalf("%s: %d g cheese left, want %d", where, ing.Left, want)
		}
	}
	for _, m := range s.Menu(false) {
		if m.Counted != e.counted[m.ID] {
			t.Fatalf("%s: %s counted %v, want %v", where, m.Name, m.Counted, e.counted[m.ID])
		}
		if !m.Counted {
			continue
		}
		want := e.base[m.ID] - (e.used(m.ID) - e.usedAt[m.ID])
		if m.Left < 0 || m.Left != want {
			t.Fatalf("%s: %s has %d left, want %d", where, m.Name, m.Left, want)
		}
	}
}
