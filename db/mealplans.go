package db

import (
	"errors"
	"strings"
	"time"

	"emeal-booking/schedule"
)

// The "3 meals" deal: which dish is meal 1, 2 and 3 is chosen per table (rice
// first, then fish…), and an order can be saved as a pattern to use again.
// Starting the deal puts its price on the table's bill, per guest.

// MealPlan is a saved order of the three meals.
type MealPlan struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Dishes []string `json:"dishes"`
}

// DealPriceCents is what the deal costs per guest until the manager changes
// it on the Menu page ("3 rätter").
const DealPriceCents = 2000

// DealCategory is the menu section the deal's price lives in.
const DealCategory = "3 rätter"

var (
	ErrPlanName   = errors.New("give the order a name")
	ErrPlanDishes = errors.New("choose a dish for each meal")
	ErrBadGuests  = errors.New("how many guests?")
)

// MaxDealGuests: more than this on one table is surely a typo.
const MaxDealGuests = 40

func cleanDishes(dishes []string) ([]string, error) {
	if strings.TrimSpace(strings.Join(dishes, "")) == "" {
		return nil, nil // not chosen: just "meal 1, 2, 3"
	}
	if len(dishes) != schedule.MealsInDeal {
		return nil, ErrPlanDishes
	}
	out := make([]string, len(dishes))
	for i, d := range dishes {
		d = strings.Join(strings.Fields(d), " ")
		if d == "" || len([]rune(d)) > 80 {
			return nil, ErrPlanDishes
		}
		out[i] = d
	}
	return out, nil
}

// MealPlans lists the saved orders, by name.
func (s *Store) MealPlans() []MealPlan {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []MealPlan
	for _, p := range s.d.MealPlans {
		out = append(out, MealPlan{ID: p.ID, Name: p.Name, Dishes: append([]string(nil), p.Dishes...)})
	}
	return out
}

// SaveMealPlan saves an order of the three meals under a name (the same name
// again replaces it).
func (s *Store) SaveMealPlan(name string, dishes []string) (MealPlan, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len([]rune(name)) > 60 {
		return MealPlan{}, ErrPlanName
	}
	d, err := cleanDishes(dishes)
	if err != nil {
		return MealPlan{}, err
	}
	if d == nil {
		return MealPlan{}, ErrPlanDishes
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.d.MealPlans {
		if strings.EqualFold(p.Name, name) {
			old := p.Dishes
			p.Name, p.Dishes = name, d
			if err := s.save(); err != nil {
				p.Dishes = old
				return MealPlan{}, err
			}
			return *p, nil
		}
	}
	s.d.NextMealPlanID++
	p := &MealPlan{ID: s.d.NextMealPlanID, Name: name, Dishes: d}
	s.d.MealPlans = append(s.d.MealPlans, p)
	if err := s.save(); err != nil {
		s.d.MealPlans = s.d.MealPlans[:len(s.d.MealPlans)-1]
		s.d.NextMealPlanID--
		return MealPlan{}, err
	}
	return *p, nil
}

// RemoveMealPlan deletes a saved order.
func (s *Store) RemoveMealPlan(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, p := range s.d.MealPlans {
		if p.ID == id {
			s.d.MealPlans = append(s.d.MealPlans[:i], s.d.MealPlans[i+1:]...)
			if err := s.save(); err != nil {
				s.d.MealPlans = append(s.d.MealPlans[:i], append([]*MealPlan{p}, s.d.MealPlans[i:]...)...)
				return err
			}
			return nil
		}
	}
	return ErrNotFound
}

// DealItem is the menu item for the deal's price ("3 rätter", staff only).
// It's made the first time it's needed, at DealPriceCents; the manager can
// change the price on the Menu page.
func (s *Store) DealItem() (MenuItem, error) {
	s.mu.Lock()
	if m := s.menuByID(s.d.DealItemID); m != nil && s.d.DealItemID != 0 {
		item := *m
		s.mu.Unlock()
		return item, nil
	}
	s.mu.Unlock()
	item, err := s.SaveMenuItem(MenuItem{Name: "3 rätter", Category: DealCategory, PriceCents: DealPriceCents,
		Description: "Pris per gäst för 3 rätter", Package: true, StaffOnly: true, Available: true})
	if err != nil {
		return MenuItem{}, err
	}
	s.mu.Lock()
	s.d.DealItemID = item.ID
	err = s.save()
	s.mu.Unlock()
	return item, err
}

// StartDealWith starts the deal on a table with the meals in the order the
// guests want, puts the price for guests guests on the table's bill
// (opening one if needed) and sends meal 1 to the chef. All or nothing.
func (s *Store) StartDealWith(table int, dishes []string, guests int, bookingRef string, deposit int, now time.Time) (Bill, error) {
	d, err := cleanDishes(dishes)
	if err != nil {
		return Bill{}, err
	}
	if guests < 1 || guests > MaxDealGuests {
		return Bill{}, ErrBadGuests
	}
	item, err := s.DealItem()
	if err != nil {
		return Bill{}, err
	}
	lines := []NewLine{{MenuItemID: item.ID, Name: item.Name, PriceCents: item.PriceCents, ListCents: item.PriceCents,
		Qty: guests, NoKitchen: true, VAT: item.VAT()}}
	var out Bill
	err = s.changeDeal(func() (bool, error) {
		r, err := s.startRun(table, now)
		if err != nil {
			return false, err
		}
		r.Dishes, r.Guests = d, guests
		if err := s.takeStock(lines); err != nil {
			return false, err
		}
		b := s.openBillAt(table)
		if b == nil {
			b = s.newBill(table, bookingRef, deposit, now)
			b.QROpen = true // the guests can join with the QR code from now on
		}
		s.addLines(b, lines, nil, false, now)
		b.BilledAt = nil
		b.Guests = max(b.Guests, guests)
		s.sendMeal(r, 1, now)
		out = b.clone()
		return true, nil
	})
	return out, err
}
