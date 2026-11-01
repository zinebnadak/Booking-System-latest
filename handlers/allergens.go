package handlers

import (
	"strings"

	"ozmo-booking/db"
)

// The 14 main allergens, in the guests' words (short, next to a dish) and in
// the manager's (with examples, on the Menu page).

type allergenWord struct{ en, sv, enLong, svLong string }

var allergenWords = map[string]allergenWord{
	"gluten":      {"gluten", "gluten", "Gluten (wheat, rye, barley, oats)", "Gluten (vete, råg, korn, havre)"},
	"crustaceans": {"crustaceans", "kräftdjur", "Crustaceans (shrimp, crab, lobster)", "Kräftdjur (räkor, krabba, hummer)"},
	"egg":         {"egg", "ägg", "Egg", "Ägg"},
	"fish":        {"fish", "fisk", "Fish", "Fisk"},
	"peanuts":     {"peanuts", "jordnötter", "Peanuts", "Jordnötter"},
	"soy":         {"soy", "soja", "Soy", "Soja"},
	"milk":        {"milk", "mjölk", "Milk (also lactose-free milk)", "Mjölk (även laktosfri mjölk)"},
	"nuts":        {"nuts", "nötter", "Nuts (almond, hazelnut, walnut, cashew…)", "Nötter (mandel, hasselnöt, valnöt, cashew …)"},
	"celery":      {"celery", "selleri", "Celery", "Selleri"},
	"mustard":     {"mustard", "senap", "Mustard", "Senap"},
	"sesame":      {"sesame", "sesam", "Sesame", "Sesam"},
	"sulphites":   {"sulphites", "sulfiter", "Sulphites (wine, dried fruit)", "Sulfiter (vin, torkad frukt)"},
	"lupin":       {"lupin", "lupin", "Lupin", "Lupin"},
	"molluscs":    {"molluscs", "blötdjur", "Molluscs (mussels, squid, snails)", "Blötdjur (musslor, bläckfisk, snäckor)"},
}

// allergenList: ["fish" "milk"] -> "fish, milk" ("fisk, mjölk").
func allergenList(lang string, codes []string) string {
	var out []string
	for _, c := range codes {
		w, ok := allergenWords[c]
		if !ok {
			continue
		}
		if lang == langSV {
			out = append(out, w.sv)
		} else {
			out = append(out, w.en)
		}
	}
	return strings.Join(out, ", ")
}

// allergenChoice is one tick box on the Menu page.
type allergenChoice struct {
	Code, Label string
	On          bool
}

func allergenChoices(lang string, have []string) []allergenChoice {
	on := map[string]bool{}
	for _, c := range have {
		on[c] = true
	}
	out := make([]allergenChoice, 0, len(db.Allergens))
	for _, c := range db.Allergens {
		w := allergenWords[c]
		label := w.enLong
		if lang == langSV {
			label = w.svLong
		}
		out = append(out, allergenChoice{Code: c, Label: label, On: on[c]})
	}
	return out
}
