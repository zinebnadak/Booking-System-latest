package db

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"emeal-booking/schedule"
)

// Stock: what's left in the storeroom, so a waiter can tell a guest "sorry,
// that's finished" before they've chosen it, not after.
//
// Any staff member (waiters, the kitchen) can, on the Stock page:
//   - tap "Out of stock": nobody can order it until it's back,
//   - type how many are left: it counts down as it's ordered, and is out of
//     stock at 0,
//   - tap "Back in stock": it's simply on the menu again, not counted.
//
// Counting happens inside the same locked step as the order, so two waiters
// can't both sell the last bottle.

// LowStock: with this many left or fewer, screens say it's running low.
const LowStock = 3

// MaxStock is the most anyone can type as "left".
const MaxStock = 9999

// NotCounted, given to SetStock, stops counting: it's simply on the menu.
const NotCounted = -1

var ErrBadStock = errors.New("how many are left must be 0 to 9999")

// ErrSoldOut: something ordered is out of stock, or not enough is left.
var ErrSoldOut = errors.New("sold out")

// SoldOutError says which item, and how many are left (0 = none). Short is
// the ingredient that isn't enough, if that's why.
type SoldOutError struct {
	Name  string
	Left  int
	Short string
}

func (e *SoldOutError) Error() string {
	if e.Left <= 0 {
		return fmt.Sprintf("%s is out of stock", e.Name)
	}
	return fmt.Sprintf("only %d left of %s", e.Left, e.Name)
}

func (e *SoldOutError) Unwrap() error { return ErrSoldOut }

// menuByID finds a menu item. Callers hold s.mu.
func (s *Store) menuByID(id int) *MenuItem {
	for _, m := range s.d.Menu {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// takeStock counts down what lines order, all or nothing: if anything
// counted hasn't enough left, nothing changes. Callers hold s.mu (inside
// changeOrders, so a later error puts the counts back too).
func (s *Store) takeStock(lines []NewLine) error {
	need := map[int]int{}
	var order []int
	for _, l := range lines {
		if l.MenuItemID == 0 || l.Qty <= 0 {
			continue
		}
		if need[l.MenuItemID] == 0 {
			order = append(order, l.MenuItemID)
		}
		need[l.MenuItemID] += l.Qty
	}
	// What the dishes take from the ingredients, all together.
	ingNeed := map[int]int{}
	for _, id := range order {
		m := s.menuByID(id)
		if m == nil {
			continue
		}
		if m.Counted && m.Left < need[id] {
			return &SoldOutError{Name: m.Name, Left: max(m.Left, 0)}
		}
		for _, u := range m.Uses {
			if s.ingredientByID(u.ID) != nil && u.Amount > 0 {
				ingNeed[u.ID] += u.Amount * need[id]
			}
		}
	}
	for _, id := range order {
		m := s.menuByID(id)
		if m == nil {
			continue
		}
		for _, u := range m.Uses {
			ing := s.ingredientByID(u.ID)
			if ing == nil || u.Amount <= 0 || ing.Left >= ingNeed[u.ID] {
				continue
			}
			// Not enough for everything ordered: say how many of this dish
			// the ingredient still allows on its own.
			return &SoldOutError{Name: m.Name, Left: max(ing.Left, 0) / u.Amount, Short: ing.Name}
		}
	}
	for _, id := range order {
		if m := s.menuByID(id); m != nil && m.Counted {
			m.Left -= need[id]
		}
	}
	for id, n := range ingNeed {
		s.ingredientByID(id).Left -= n
	}
	return nil
}

// giveStock puts n back (an order taken off the bill, or rejected).
// Callers hold s.mu.
func (s *Store) giveStock(menuID, n int) {
	m := s.menuByID(menuID)
	if m == nil || n <= 0 {
		return
	}
	if m.Counted {
		m.Left = min(m.Left+n, MaxStock)
	}
	for _, u := range m.Uses {
		if ing := s.ingredientByID(u.ID); ing != nil && u.Amount > 0 {
			ing.Left = min(ing.Left+u.Amount*n, MaxIngredientLeft)
		}
	}
}

// giveLines puts back everything lines ordered. Callers hold s.mu.
func (s *Store) giveLines(lines []NewLine) {
	for _, l := range lines {
		s.giveStock(l.MenuItemID, l.Qty)
	}
}

// SetStock sets how many are left (0 = out of stock), or with NotCounted
// stops counting. Anything but "out" also takes away the kitchen's "sold
// out today": it's there again.
func (s *Store) SetStock(id, left int) (MenuItem, error) {
	if left != NotCounted && (left < 0 || left > MaxStock) {
		return MenuItem{}, ErrBadStock
	}
	var out MenuItem
	err := s.changeOrders(func() (bool, error) {
		m := s.menuByID(id)
		if m == nil {
			return false, ErrNotFound
		}
		if left == NotCounted {
			m.Counted, m.Left = false, 0
		} else {
			m.Counted, m.Left = true, left
		}
		if left != 0 {
			m.SoldOutOn = ""
		}
		out = *m
		out.Allergens = append([]string(nil), m.Allergens...)
		return true, nil
	})
	return out, err
}

// StockLists are what's out of stock (or sold out today) and what's
// running low, of the items switched on, in menu order.
func (s *Store) StockLists(now time.Time) (out, low []MenuItem) {
	today := schedule.FormatDate(schedule.Today(now))
	for _, m := range s.Menu(false) {
		if !m.Active(today) {
			continue
		}
		switch {
		case m.Gone(today):
			out = append(out, m)
		case m.RunningLow():
			low = append(low, m)
		}
	}
	sort.SliceStable(low, func(i, j int) bool { return low[i].LeftNow() < low[j].LeftNow() })
	return out, low
}

// stockSnap is a menu item's count, for changeOrders to put back.
type stockSnap struct {
	id      int
	counted bool
	left    int
	soldOut string
	uses    []IngredientUse
}

// CheckStock says whether lines could be ordered right now, without taking
// anything (a takeaway order before it's paid).
func (s *Store) CheckStock(lines []NewLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved := s.stockSnapshot()
	err := s.takeStock(lines)
	s.restoreStock(saved)
	return err
}

type stockSaved struct {
	left map[int]int
	ing  map[int]int
}

func (s *Store) stockSnapshot() stockSaved {
	v := stockSaved{left: map[int]int{}, ing: map[int]int{}}
	for i := range s.d.Menu {
		v.left[s.d.Menu[i].ID] = s.d.Menu[i].Left
	}
	for i := range s.d.Ingredients {
		v.ing[s.d.Ingredients[i].ID] = s.d.Ingredients[i].Left
	}
	return v
}

func (s *Store) restoreStock(v stockSaved) {
	for i := range s.d.Menu {
		s.d.Menu[i].Left = v.left[s.d.Menu[i].ID]
	}
	for i := range s.d.Ingredients {
		s.d.Ingredients[i].Left = v.ing[s.d.Ingredients[i].ID]
	}
}

// takeStockPaid counts down an order that is already paid (takeaway): it
// goes through even if the last one went meanwhile, and nothing goes below
// 0. Callers hold s.mu.
func (s *Store) takeStockPaid(lines []NewLine) {
	for _, l := range lines {
		m := s.menuByID(l.MenuItemID)
		if m == nil || l.Qty <= 0 {
			continue
		}
		if m.Counted {
			m.Left = max(m.Left-l.Qty, 0)
		}
		for _, u := range m.Uses {
			if ing := s.ingredientByID(u.ID); ing != nil && u.Amount > 0 {
				ing.Left = max(ing.Left-u.Amount*l.Qty, 0)
			}
		}
	}
}

// takeawayLines are a takeaway order's dishes as order lines.
func takeawayLines(b Booking) []NewLine {
	var out []NewLine
	for _, it := range b.Items {
		out = append(out, NewLine{MenuItemID: it.MenuItemID, Name: it.Name, Qty: it.Quantity})
	}
	return out
}
