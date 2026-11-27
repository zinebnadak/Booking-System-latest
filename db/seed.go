package db

// The restaurant's menu, copied from its printed menus (November 2026:
// "Schnitzelveckor" and "Dryckesmeny"). It's
// the starting menu of a new data file, and it replaces the old made-up demo
// menu once (see upgradeMenu); after that the manager changes it from the
// Menu page.
//
// Prices that weren't on the printed menus are 0,00 €: those dishes are
// marked "price missing" on the Menu page, staff can still put them on a bill
// (and type the price), and guests never see them until a price is set.
//
// Tags as on the printed menu: L = laktosfri, G = glutenfri, M = mjölkfri,
// * = can be made that way.

// menuVersion is the version of seedMenu. Data files with an older version
// that still have the untouched demo menu get this one instead.
const menuVersion = 3

// Courses: when the kitchen makes a dish (see the hold-the-mains buttons).
const (
	CourseNone    = 0 // drinks: not cooked
	CourseStarter = 1
	CourseMain    = 2
	CourseDessert = 3
)

func seedMenu() []MenuItem {
	var items []MenuItem
	add := func(cat string, course int, drink, alcohol bool, list ...MenuItem) {
		for _, it := range list {
			it.Category, it.Course, it.Drink, it.Alcohol = cat, course, drink, alcohol
			items = append(items, it)
		}
	}
	food := func(cat string, course int, list ...MenuItem) { add(cat, course, false, false, list...) }
	drink := func(cat string, alcohol bool, list ...MenuItem) { add(cat, CourseNone, true, alcohol, list...) }

	// ---- Food (the printed "Schnitzelveckor" menu) ----
	food("Förrätt", CourseStarter,
		MenuItem{Name: "Jordärtskockssoppa", Description: "Med rostat bacon", PriceCents: 1090},
		MenuItem{Name: "Liten toast skagen", PriceCents: 1590},
		MenuItem{Name: "Råbiff", Description: "Med tryffelaioli, parmesan, rostade nötter, rotsakschips och picklad rödlök", PriceCents: 1790},
	)
	// A new schnitzel every week ("Veckans schnitzel från 22,50 €"): switch on this week's.
	food("Schnitzelveckor", CourseMain,
		MenuItem{Name: "Jägarschnitzel", Description: "Fläskschnitzel med krämig jägersås, sidosallad och pommes crispers", PriceCents: 2250, Tags: "L"},
		MenuItem{Name: "Cordon bleu", Description: "Fläskschnitzel fylld med ost och skinka. Serveras med krämig cheddarsås, sidosallad och pommes crispers", PriceCents: 2550, Tags: "L"},
		MenuItem{Name: "Wienerschnitzel på oxfilé", Description: "Panerad oxfilé med citron, ansjovis, kapris och rödvinssky. Serveras med sidosallad och pommes crispers", PriceCents: 3990, Tags: "L"},
		MenuItem{Name: "Parmesankycklingschnitzel", Description: "Panerad kycklingschnitzel med chilibea och parmesan. Serveras med sidosallad och pommes crispers", PriceCents: 2490, Tags: "L"},
	)
	food("Pasta", CourseMain,
		MenuItem{Name: "Oxfilépasta med tryffel", Description: "Med oxfilé, svamp, pepparsås, parmesan, rödlök och tryffelolja", PriceCents: 2390, Tags: "L"},
		MenuItem{Name: "Pasta carbonara", Description: "Med bacon, parmesan, rödlök, balsamicosirap och ruccola", PriceCents: 1890, Tags: "L"},
		MenuItem{Name: "Kyckling pomodoro pasta", Description: "Med kyckling, E-Meal's pomodorosås, parmesan, ruccola och balsamicosirap", PriceCents: 2290, Tags: "L"},
	)
	food("Klassiska rätter", CourseMain,
		MenuItem{Name: "Jägarsmörgås", Description: "Fläskschnitzel på stekt bröd med champinjonsås, salladsbädd och pommes crispers", PriceCents: 2550, Tags: "L"},
		MenuItem{Name: "Stor toast skagen", Description: "Skagenröra, stekt bröd, körsbärstomater, picklad rödlök, salladsbädd och citron", PriceCents: 2190, Tags: "M"},
		MenuItem{Name: "Oxfilé", Description: "Med rödvinssky, bearnaise och pommes crispers", PriceCents: 3990, Tags: "G, L"},
	)
	food("Burger", CourseMain,
		MenuItem{Name: "E-Meal's chipotle bacon burgare", Description: "Med smashed nötfärs, cheddarost, bacon, chipotlemajo, sidosallad och pommes crispers", PriceCents: 1990, Tags: "L"},
	)
	food("Sallad & bowl", CourseMain,
		MenuItem{Name: "Caesarsallad", Description: "Med kyckling, bacon, parmesanaioli, körsbärstomater, rödlök, romansallad, bröd, färskost och krutonger. Går att få glutenfri", PriceCents: 1990, Tags: "L, *G"},
		MenuItem{Name: "Spicy chicken bowl", Description: "Med spicy kycklingröra, kålslaw, vattenmelon, rostade frön och ris", PriceCents: 1990, Tags: "G, M"},
	)
	food("Barnmeny", CourseMain,
		MenuItem{Name: "Pasta bolognese (barn)", PriceCents: 1190},
		MenuItem{Name: "Pasta carbonara (barn)", PriceCents: 1190},
		MenuItem{Name: "Korv med pommes", PriceCents: 1190},
		MenuItem{Name: "Barnburgare med pommes", PriceCents: 1190},
		MenuItem{Name: "Chicken fingers med pommes", PriceCents: 1190},
	)
	food("Dessert", CourseDessert,
		MenuItem{Name: "Chokladmousse", Description: "Med rostade nötter och vispad grädde", PriceCents: 890},
		MenuItem{Name: "Vaniljglass", Description: "Med kinuskisås och hallonkompott", PriceCents: 990},
	)

	// ---- Drinks (the printed "Dryckesmeny"; tables only, never to the chef) ----
	// Wine and bubbles: a glass and a bottle, as the two prices on the menu.
	drink("Cocktails", true,
		MenuItem{Name: "Aperol spritz", Description: "Aperol, prosecco, soda och apelsin", PriceCents: 990},
		MenuItem{Name: "Limoncello spritz", Description: "Limoncello, prosecco, soda och citron", PriceCents: 990},
		MenuItem{Name: "Moscow mule", Description: "Vodka, ginger beer och lime", PriceCents: 990},
		MenuItem{Name: "Elderflower", Description: "St-Germain, gin, sockerlag och citron", PriceCents: 990},
		MenuItem{Name: "Raspberry summer", Description: "Vodka, hallonsockerlag, citron och Sprite", PriceCents: 990},
		MenuItem{Name: "Passion pear", Description: "Xanté, limoncello, passionsjuice, citron och soda eller Sprite", PriceCents: 990},
		MenuItem{Name: "Salt lakrits lemonade", Description: "Ga-Jol Salt Lakrits, citron och Sprite", PriceCents: 990},
	)
	drink("Kaffedrinkar", true,
		MenuItem{Name: "Irish coffee", Description: "Irish whiskey, kaffe och vispad grädde", PriceCents: 1090},
		MenuItem{Name: "Spanish coffee", Description: "Likör 43, kaffe och vispad grädde", PriceCents: 1090},
	)
	drink("Bubbel", true,
		MenuItem{Name: "Arthur Metz Crémant, glas", PriceCents: 1050},
		MenuItem{Name: "Arthur Metz Crémant, flaska", PriceCents: 4000},
		MenuItem{Name: "Prosecco Rocca 8, glas", PriceCents: 1050},
		MenuItem{Name: "Prosecco Rocca 8, flaska", PriceCents: 4000},
		MenuItem{Name: "Moët & Chandon Impérial, flaska", PriceCents: 8500},
	)
	drink("Vitvin", true,
		MenuItem{Name: "Yellow Tail Pinot Grigio, glas", PriceCents: 950},
		MenuItem{Name: "Yellow Tail Pinot Grigio, flaska", PriceCents: 3600},
		MenuItem{Name: "Yellow Tail Sauvignon Blanc, glas", PriceCents: 1150},
		MenuItem{Name: "Yellow Tail Sauvignon Blanc, flaska", PriceCents: 3600},
		MenuItem{Name: "Kung Fu Girl Riesling, glas", PriceCents: 1290},
		MenuItem{Name: "Kung Fu Girl Riesling, flaska", PriceCents: 4800},
		MenuItem{Name: "Moillard Petit Chablis, flaska", PriceCents: 5400},
	)
	drink("Rödvin", true,
		MenuItem{Name: "Yellow Tail Shiraz, glas", PriceCents: 950},
		MenuItem{Name: "Yellow Tail Shiraz, flaska", PriceCents: 3600},
		MenuItem{Name: "Cuma Malbec, glas", PriceCents: 1190},
		MenuItem{Name: "Cuma Malbec, flaska", PriceCents: 4200},
		MenuItem{Name: "Gnarly Head Old Vine Zinfandel, glas", PriceCents: 1350},
		MenuItem{Name: "Gnarly Head Old Vine Zinfandel, flaska", PriceCents: 4600},
		MenuItem{Name: "Roberto Sarotto Barolo, flaska", PriceCents: 7500},
	)
	drink("Öl", true,
		MenuItem{Name: "Heineken 0,33 l", PriceCents: 700},
		MenuItem{Name: "Heineken 0,5 l", PriceCents: 900},
		MenuItem{Name: "Aura 0,33 l", PriceCents: 700},
		MenuItem{Name: "Aura 0,5 l", PriceCents: 900},
		MenuItem{Name: "Stallhagen Original 0,33 l", PriceCents: 800},
		MenuItem{Name: "Stallhagen Honung 0,33 l", PriceCents: 800},
		MenuItem{Name: "Stallhagen Delikat 0,33 l", PriceCents: 800},
	)
	drink("Cider & longdrink", true,
		MenuItem{Name: "Briska fläder", PriceCents: 800},
		MenuItem{Name: "Happy Joe äppel", PriceCents: 800},
		MenuItem{Name: "Happy Joe päron", PriceCents: 800},
		MenuItem{Name: "Longdrink", PriceCents: 800},
	)
	// Up to 1,2 % alcohol counts as alcohol-free (food VAT).
	drink("Alkoholfritt", false,
		MenuItem{Name: "Stallhagen Styrman", PriceCents: 600},
		MenuItem{Name: "Heineken 0,0 %", PriceCents: 600},
		MenuItem{Name: "Briska fläder 0,5 %", PriceCents: 600},
	)
	drink("Läsk & juice", false,
		MenuItem{Name: "Coca-Cola", PriceCents: 350},
		MenuItem{Name: "Coca-Cola Zero", PriceCents: 350},
		MenuItem{Name: "Jaffa", PriceCents: 350},
		MenuItem{Name: "Sprite Zero", PriceCents: 350},
		MenuItem{Name: "Grannas äppeljuice", PriceCents: 350},
	)

	// Only this week's schnitzel is on; the manager switches them weekly.
	off := map[string]bool{"Cordon bleu": true, "Wienerschnitzel på oxfilé": true, "Parmesankycklingschnitzel": true}
	for i := range items {
		items[i].Available = !off[items[i].Name]
		items[i].Sort = (i + 1) * 10 // gaps of 10 make re-ordering easy later
	}
	return items
}

// demoMenuNames is the made-up menu the first versions started with.
var demoMenuNames = map[string]bool{
	"Toast skagen": true, "Rödbetssallad": true, "Fisksoppa": true, "Kycklinglårfilé": true,
	"Laxfilé": true, "Svamprisotto": true, "E-Meal-burgare": true, "Chokladfondant": true,
	"Pannacotta": true, "Sorbet": true, "Läsk 33 cl": true, "Mineralvatten": true,
	"Kaffe": true, "Husets vin, glas": true,
}

// upgradeMenu brings an older data file's menu up to date, once:
//   - the made-up demo menu (and nothing else) is replaced by seedMenu;
//     any other menu from before version 2 is the manager's and left alone;
//   - version 2 (the October menus) becomes the November menus: see menuV3.
//
// Callers hold s.mu.
func (s *Store) upgradeMenu() bool {
	if s.d.MenuVersion >= menuVersion {
		return false
	}
	from := s.d.MenuVersion
	s.d.MenuVersion = menuVersion
	if from >= 2 {
		s.menuV3()
		return true
	}
	demo := len(s.d.Menu) > 0
	for _, m := range s.d.Menu {
		demo = demo && demoMenuNames[m.Name]
	}
	if !demo {
		return true // a real menu: leave it, just remember the check was done
	}
	s.d.Menu = nil
	for _, it := range seedMenu() {
		it := it
		it.ID = s.d.NextMenuID
		s.d.NextMenuID++
		s.d.Menu = append(s.d.Menu, &it)
	}
	return true
}

// Menu version 2 to 3: dishes renamed on the November menus.
var renamedV3 = map[string]string{
	"Chipotle bacon burgare":               "E-Meal's chipotle bacon burgare",
	"Happy Joe äpple":                      "Happy Joe äppel",
	"Arthur Metz Crémant 12 cl":            "Arthur Metz Crémant, glas",
	"Prosecco Rocca 8, 12 cl":              "Prosecco Rocca 8, glas",
	"Yellow Tail Pinot Grigio 16 cl":       "Yellow Tail Pinot Grigio, glas",
	"Yellow Tail Sauvignon Blanc 16 cl":    "Yellow Tail Sauvignon Blanc, glas",
	"Kung Fu Girl Riesling 16 cl":          "Kung Fu Girl Riesling, glas",
	"Yellow Tail Shiraz 16 cl":             "Yellow Tail Shiraz, glas",
	"Cuma Malbec 16 cl":                    "Cuma Malbec, glas",
	"Gnarly Head Old Vine Zinfandel 16 cl": "Gnarly Head Old Vine Zinfandel, glas",
}

// Menu version 2 to 3: version 2's dishes that aren't on the November menus.
var droppedV3 = map[string]bool{
	// Tillbehör & dippar
	"Extra biff": true, "Byt till sötpotatispommes": true, "Pommes crispers": true, "Sötpotatispommes": true,
	"Lökringar": true, "Mozzarella sticks": true, "Chili cheese balls": true, "Chicken fingers": true,
	"Chipotledipp": true, "Tryffeldipp": true, "Aioli": true, "Vegansk aioli": true, "Bearnaise": true,
	"Rödvinssky": true, "Pepparsås": true,
	// Lunch
	"Dagens lunch": true, "Dagens vegetariska": true, "Veckans schnitzel (lunch)": true, "Veckans fisk": true,
	"Veckans sallad": true, "Lunch carbonara": true, "Lunch caesarsallad": true, "Lunch spicy chicken bowl": true,
	"Lunch burgare": true, "Husets lunch": true, "Barnlunch": true,
	// After Work
	"After Work · 3 rätter": true, "After Work · 1 rätt": true, "After Work · dryck": true,
	"After Work · alkoholfri dryck": true, "AW Hummersoppa": true, "AW Blue cheese smokey BBQ-burgare": true,
	"AW Jägarschnitzel": true, "AW Stor toast skagen": true, "AW Jordärtskocksrisotto": true,
	"AW Passionsfruktcheesecake": true,
	// Wine by 24 cl (no price on the menu)
	"Yellow Tail Pinot Grigio 24 cl": true, "Yellow Tail Sauvignon Blanc 24 cl": true, "Kung Fu Girl Riesling 24 cl": true,
	"Yellow Tail Shiraz 24 cl": true, "Cuma Malbec 24 cl": true, "Gnarly Head Old Vine Zinfandel 24 cl": true,
	// Kaffe & te
	"Kaffe": true, "Te": true, "Espresso": true, "Espresso doppio": true, "Cappuccino": true, "Caffè latte": true,
}

// menuV3 makes a version 2 menu the November menus, in place: each dish
// keeps its id (so bills, stock and photos still point to it), its photo,
// allergens and whether it's on today, and gets the printed name, text,
// price, section and order. Dishes no longer printed are removed; dishes
// the manager added themselves are kept. Callers hold s.mu.
func (s *Store) menuV3() {
	want := seedMenu()
	byName := map[string]int{}
	for i, it := range want {
		byName[it.Name] = i
	}
	seen := map[string]bool{}
	var menu []*MenuItem
	for _, m := range s.d.Menu {
		name := m.Name
		if n, ok := renamedV3[name]; ok {
			name = n
		}
		i, printed := byName[name]
		switch {
		case printed && !seen[name]:
			w := want[i]
			m.Name, m.Description, m.PriceCents, m.Tags = w.Name, w.Description, w.PriceCents, w.Tags
			m.Category, m.Sort, m.Course, m.Drink, m.Alcohol = w.Category, w.Sort, w.Course, w.Drink, w.Alcohol
			m.Package, m.Included, m.StaffOnly = false, false, false
			seen[name] = true
			menu = append(menu, m)
		case droppedV3[m.Name] || printed:
			// not on the menus any more (or a second copy): removed
		default:
			menu = append(menu, m) // the manager's own dish
		}
	}
	for _, w := range want {
		if seen[w.Name] {
			continue
		}
		it := w
		it.ID = s.d.NextMenuID
		s.d.NextMenuID++
		menu = append(menu, &it)
	}
	s.d.Menu = menu
}
