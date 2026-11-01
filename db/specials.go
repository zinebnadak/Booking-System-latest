package db

import (
	"errors"
	"strings"
)

// Specials: a dish typed in on the order form ("Something not on the menu")
// that staff keep on the menu for a while: today only, or until a day. It
// goes in its own section at the top of the order form and is off the menu
// by itself after its last day (MenuItem.Until). Guests don't see it on
// their phones (it has no allergens filled in); the manager can change that
// on the Menu page.

// SpecialsCategory is the menu section specials go in.
const SpecialsCategory = "Dagens special"

var ErrBadSpecial = errors.New("a special needs a name, a price and a last day from today on")

// SaveSpecial puts a special on the menu, or, if one with the same name is
// already there, updates it (new price, last day, ingredients).
func (s *Store) SaveSpecial(item MenuItem) (MenuItem, error) {
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" || item.PriceCents <= 0 || item.Until == "" {
		return MenuItem{}, ErrBadSpecial
	}
	item.Category, item.Available, item.StaffOnly, item.Sort = SpecialsCategory, true, true, -100
	if !item.Drink {
		item.Course = CourseMain
	}
	s.mu.Lock()
	var existing *MenuItem
	for _, m := range s.d.Menu {
		if m.Category == SpecialsCategory && strings.EqualFold(m.Name, item.Name) {
			existing = m
		}
	}
	if existing != nil {
		item.ID = existing.ID
		item.Description, item.Allergens, item.Photo = existing.Description, existing.Allergens, existing.Photo
		item.Tags = existing.Tags
	}
	s.mu.Unlock()
	return s.SaveMenuItem(item)
}
