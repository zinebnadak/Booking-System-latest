package db

import (
	"errors"
	"sort"
	"strings"
)

// Ingredients in stock: spaghetti, cheese, salmon… Staff keep count of each
// on the Stock page, in its own unit (g, st, l). A dish says what one
// portion uses (Uses). Ordering it takes that from stock, giving it back
// counts it back, and when an ingredient runs out every dish that needs it
// is out of stock at once.

// Ingredient is one thing in stock.
type Ingredient struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Unit  string `json:"unit"`             // "g", "st", "l"…
	Left  int    `json:"left"`             // how much is left, in Unit
	LowAt int    `json:"low_at,omitempty"` // "running low" at this or less (0 = only when out)
}

// Low: at or under its warning level (but not out).
func (i Ingredient) Low() bool { return i.Left > 0 && i.LowAt > 0 && i.Left <= i.LowAt }

// IngredientUse is how much of an ingredient one portion of a dish takes.
type IngredientUse struct {
	ID     int `json:"id"`
	Amount int `json:"amount"`
}

// MaxIngredientLeft is the most anyone can type (99 999 g is 100 kg).
const MaxIngredientLeft = 9_999_999

var (
	ErrBadIngredient  = errors.New("an ingredient needs a name, and amounts from 0 to 9 999 999")
	ErrIngredientName = errors.New("there is already an ingredient with that name")
)

func (s *Store) ingredientByID(id int) *Ingredient {
	for _, i := range s.d.Ingredients {
		if i.ID == id {
			return i
		}
	}
	return nil
}

// fillCanMake works out, for a copy of a dish, how many its ingredients
// still allow, and which one ran out. Callers hold s.mu.
func (s *Store) fillCanMake(m *MenuItem) {
	m.Limited, m.CanMake, m.ShortOf = false, 0, ""
	for _, u := range m.Uses {
		ing := s.ingredientByID(u.ID)
		if ing == nil || u.Amount <= 0 {
			continue
		}
		can := max(ing.Left, 0) / u.Amount
		if !m.Limited || can < m.CanMake {
			m.CanMake = can
		}
		m.Limited = true
		if can == 0 && m.ShortOf == "" {
			m.ShortOf = ing.Name
		}
	}
}

// cleanUses keeps uses of ingredients that exist, with an amount, once
// each. Callers hold s.mu.
func (s *Store) cleanUses(uses []IngredientUse) []IngredientUse {
	var out []IngredientUse
	seen := map[int]bool{}
	for _, u := range uses {
		if u.Amount <= 0 || u.Amount > MaxIngredientLeft || seen[u.ID] || s.ingredientByID(u.ID) == nil {
			continue
		}
		seen[u.ID] = true
		out = append(out, u)
	}
	return out
}

// Ingredients lists what's in stock, by name.
func (s *Store) Ingredients() []Ingredient {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Ingredient
	for _, i := range s.d.Ingredients {
		out = append(out, *i)
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	return out
}

func cleanIngredient(name, unit string, left, lowAt int) (string, string, error) {
	name, unit = strings.TrimSpace(name), strings.TrimSpace(unit)
	if name == "" || len([]rune(name)) > 60 || len([]rune(unit)) > 12 ||
		left < 0 || left > MaxIngredientLeft || lowAt < 0 || lowAt > MaxIngredientLeft {
		return "", "", ErrBadIngredient
	}
	return name, unit, nil
}

// AddIngredient puts a new ingredient in stock.
func (s *Store) AddIngredient(name, unit string, left, lowAt int) (Ingredient, error) {
	name, unit, err := cleanIngredient(name, unit, left, lowAt)
	if err != nil {
		return Ingredient{}, err
	}
	var out Ingredient
	err = s.changeOrders(func() (bool, error) {
		for _, i := range s.d.Ingredients {
			if strings.EqualFold(i.Name, name) {
				return false, ErrIngredientName
			}
		}
		s.d.NextIngredientID++
		i := &Ingredient{ID: s.d.NextIngredientID, Name: name, Unit: unit, Left: left, LowAt: lowAt}
		s.d.Ingredients = append(s.d.Ingredients, i)
		out = *i
		return true, nil
	})
	return out, err
}

// SetIngredient sets how much is left (a delivery came, or it was counted)
// and when it counts as running low.
func (s *Store) SetIngredient(id, left, lowAt int) (Ingredient, error) {
	if _, _, err := cleanIngredient("x", "", left, lowAt); err != nil {
		return Ingredient{}, err
	}
	var out Ingredient
	err := s.changeOrders(func() (bool, error) {
		i := s.ingredientByID(id)
		if i == nil {
			return false, ErrNotFound
		}
		i.Left, i.LowAt = left, lowAt
		out = *i
		return true, nil
	})
	return out, err
}

// RemoveIngredient takes an ingredient out of stock, and off every dish
// that used it.
func (s *Store) RemoveIngredient(id int) (Ingredient, error) {
	var out Ingredient
	err := s.changeOrders(func() (bool, error) {
		for k, i := range s.d.Ingredients {
			if i.ID != id {
				continue
			}
			out = *i
			s.d.Ingredients = append(s.d.Ingredients[:k], s.d.Ingredients[k+1:]...)
			for _, m := range s.d.Menu {
				var keep []IngredientUse
				for _, u := range m.Uses {
					if u.ID != id {
						keep = append(keep, u)
					}
				}
				m.Uses = keep
			}
			return true, nil
		}
		return false, ErrNotFound
	})
	return out, err
}

// DishesUsing lists the names of the dishes that use each ingredient.
func (s *Store) DishesUsing() map[int][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int][]string{}
	for _, m := range s.d.Menu {
		for _, u := range m.Uses {
			out[u.ID] = append(out[u.ID], m.Name)
		}
	}
	return out
}
