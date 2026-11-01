package db

// The restaurant's menu, copied from its printed menus (October 2026). It's
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
const menuVersion = 2

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

	// ---- Food ----
	food("Förrätter", CourseStarter,
		MenuItem{Name: "Jordärtskockssoppa", Description: "Med rostat bacon", PriceCents: 1090},
		MenuItem{Name: "Liten toast skagen", Description: "Skagenröra på stekt bröd", PriceCents: 1590},
		MenuItem{Name: "Råbiff", Description: "Med tryffelaioli, parmesan, rostade nötter, rotsakschips och picklad rödlök", PriceCents: 1790},
	)
	// One schnitzel a week ("Veckans schnitzel"): switch on this week's.
	food("Veckans schnitzel", CourseMain,
		MenuItem{Name: "Jägarschnitzel", Description: "Fläskschnitzel med krämig jägersås, sidosallad och pommes crispers", PriceCents: 2250, Tags: "L"},
		MenuItem{Name: "Cordon bleu", Description: "Fläskschnitzel fylld med ost och skinka. Serveras med krämig cheddarsås, sidosallad och pommes crispers", PriceCents: 2550, Tags: "L"},
		MenuItem{Name: "Wienerschnitzel på oxfilé", Description: "Panerad oxfilé med citron, ansjovis, kapris och rödvinssky. Serveras med sidosallad och pommes crispers", PriceCents: 3990, Tags: "L"},
		MenuItem{Name: "Parmesankycklingschnitzel", Description: "Panerad kycklingschnitzel med chilibea och parmesan. Serveras med sidosallad och pommes crispers", PriceCents: 2490, Tags: "L"},
	)
	food("Pasta", CourseMain,
		MenuItem{Name: "Oxfilépasta med tryffel", Description: "Med oxfilé, svamp, pepparsås, parmesan, rödlök och tryffelolja", PriceCents: 2390, Tags: "L"},
		MenuItem{Name: "Pasta carbonara", Description: "Med bacon, parmesan, rödlök, balsamicosirap och ruccola", PriceCents: 1890, Tags: "L"},
		MenuItem{Name: "Kyckling pomodoro pasta", Description: "Med kyckling, husets pomodorosås, parmesan, ruccola och balsamicosirap", PriceCents: 2290, Tags: "L"},
	)
	food("Klassiska rätter", CourseMain,
		MenuItem{Name: "Jägarsmörgås", Description: "Fläskschnitzel på stekt bröd med champinjonsås, salladsbädd och pommes crispers", PriceCents: 2550, Tags: "L"},
		MenuItem{Name: "Stor toast skagen", Description: "Skagenröra, stekt bröd, körsbärstomater, picklad rödlök, salladsbädd och citron", PriceCents: 2190, Tags: "M"},
		MenuItem{Name: "Oxfilé", Description: "Med rödvinssky, bearnaise och pommes crispers", PriceCents: 3990, Tags: "G, L"},
	)
	food("Burgare", CourseMain,
		MenuItem{Name: "Chipotle bacon burgare", Description: "Med smashed nötfärs, cheddarost, bacon, chipotlemajo, sidosallad och pommes crispers", PriceCents: 1990, Tags: "L"},
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
	// Sides and dips go out with the mains. Prices weren't on the photos.
	food("Tillbehör & dippar", CourseMain,
		MenuItem{Name: "Extra biff"},
		MenuItem{Name: "Byt till sötpotatispommes"},
		MenuItem{Name: "Pommes crispers"},
		MenuItem{Name: "Sötpotatispommes"},
		MenuItem{Name: "Lökringar"},
		MenuItem{Name: "Mozzarella sticks"},
		MenuItem{Name: "Chili cheese balls"},
		MenuItem{Name: "Chicken fingers"},
		MenuItem{Name: "Chipotledipp"},
		MenuItem{Name: "Tryffeldipp"},
		MenuItem{Name: "Aioli"},
		MenuItem{Name: "Vegansk aioli"},
		MenuItem{Name: "Bearnaise"},
		MenuItem{Name: "Rödvinssky"},
		MenuItem{Name: "Pepparsås"},
	)
	// Lunch (weekdays): the dishes change, so the prices are set by the manager.
	food("Lunch", CourseMain,
		MenuItem{Name: "Dagens lunch"},
		MenuItem{Name: "Dagens vegetariska"},
		MenuItem{Name: "Veckans schnitzel (lunch)"},
		MenuItem{Name: "Veckans fisk"},
		MenuItem{Name: "Veckans sallad"},
		MenuItem{Name: "Lunch carbonara"},
		MenuItem{Name: "Lunch caesarsallad"},
		MenuItem{Name: "Lunch spicy chicken bowl"},
		MenuItem{Name: "Lunch burgare"},
		MenuItem{Name: "Husets lunch"},
		MenuItem{Name: "Barnlunch"},
	)

	// After Work (from the printed After Work menu): the set menu lines have
	// the price and never go to the chef; the waiter adds the dishes the
	// guest picks, which are in the price. Staff only: guests order it from
	// the waiter. The 15 € + 5 € split of "1 rätt + 1 dryck 20 €" follows the
	// old till's "AW Dryck 5" button: check it with the owner.
	setMenu := func(list ...MenuItem) {
		for _, it := range list {
			it.Category, it.StaffOnly = "After Work", true
			items = append(items, it)
		}
	}
	setMenu(
		MenuItem{Name: "After Work · 3 rätter", Description: "Förrätt, varmrätt och dessert: lägg till rätterna gästen väljer nedan", PriceCents: 3000, Package: true},
		MenuItem{Name: "After Work · 1 rätt", Description: "Med en dryck: lägg också till After Work · dryck (20 € tillsammans)", PriceCents: 1500, Package: true},
		MenuItem{Name: "After Work · dryck", Description: "Dryck med alkohol till After Work-menyn (3 rätter + dryck 35 €)", PriceCents: 500, Drink: true, Alcohol: true},
		MenuItem{Name: "After Work · alkoholfri dryck", Description: "Alkoholfri dryck till After Work-menyn", PriceCents: 500, Drink: true},
		MenuItem{Name: "AW Hummersoppa", Description: "Mustig hummersoppa med surdegsbröd och vispat smör", Included: true, Course: CourseStarter},
		MenuItem{Name: "AW Blue cheese smokey BBQ-burgare", Description: "Med smashad biff, cheddarost, picklad rödlök, sidosallad, aioli och pommes", Included: true, Course: CourseMain},
		MenuItem{Name: "AW Jägarschnitzel", Description: "Med svampsås, sidosallad och pommes", Tags: "L", Included: true, Course: CourseMain},
		MenuItem{Name: "AW Stor toast skagen", Description: "Med sidosallad och citron", Included: true, Course: CourseMain},
		MenuItem{Name: "AW Jordärtskocksrisotto", Description: "Med parmesan, körsbärstomater och balsamico", Included: true, Course: CourseMain},
		MenuItem{Name: "AW Passionsfruktcheesecake", Included: true, Course: CourseDessert},
	)

	// ---- Drinks (tables only, never to the chef) ----
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
		MenuItem{Name: "Arthur Metz Crémant 12 cl", PriceCents: 1050},
		MenuItem{Name: "Arthur Metz Crémant, flaska", PriceCents: 4000},
		MenuItem{Name: "Prosecco Rocca 8, 12 cl", PriceCents: 1050},
		MenuItem{Name: "Prosecco Rocca 8, flaska", PriceCents: 4000},
		MenuItem{Name: "Moët & Chandon Impérial, flaska", PriceCents: 8500},
	)
	drink("Vitt vin", true,
		MenuItem{Name: "Yellow Tail Pinot Grigio 16 cl", PriceCents: 950},
		MenuItem{Name: "Yellow Tail Pinot Grigio 24 cl"},
		MenuItem{Name: "Yellow Tail Pinot Grigio, flaska", PriceCents: 3600},
		MenuItem{Name: "Yellow Tail Sauvignon Blanc 16 cl", PriceCents: 1150},
		MenuItem{Name: "Yellow Tail Sauvignon Blanc 24 cl"},
		MenuItem{Name: "Yellow Tail Sauvignon Blanc, flaska", PriceCents: 3600},
		MenuItem{Name: "Kung Fu Girl Riesling 16 cl", PriceCents: 1290},
		MenuItem{Name: "Kung Fu Girl Riesling 24 cl"},
		MenuItem{Name: "Kung Fu Girl Riesling, flaska", PriceCents: 4800},
		MenuItem{Name: "Moillard Petit Chablis, flaska", PriceCents: 5400},
	)
	drink("Rött vin", true,
		MenuItem{Name: "Yellow Tail Shiraz 16 cl", PriceCents: 950},
		MenuItem{Name: "Yellow Tail Shiraz 24 cl"},
		MenuItem{Name: "Yellow Tail Shiraz, flaska", PriceCents: 3600},
		MenuItem{Name: "Cuma Malbec 16 cl", PriceCents: 1190},
		MenuItem{Name: "Cuma Malbec 24 cl"},
		MenuItem{Name: "Cuma Malbec, flaska", PriceCents: 4200},
		MenuItem{Name: "Gnarly Head Old Vine Zinfandel 16 cl", PriceCents: 1350},
		MenuItem{Name: "Gnarly Head Old Vine Zinfandel 24 cl"},
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
		MenuItem{Name: "Happy Joe äpple", PriceCents: 800},
		MenuItem{Name: "Happy Joe päron", PriceCents: 800},
		MenuItem{Name: "Longdrink", PriceCents: 800},
	)
	// Up to 1,2 % alcohol counts as alcohol-free (food VAT).
	drink("Alkoholfritt", false,
		MenuItem{Name: "Stallhagen Styrman", PriceCents: 600},
		MenuItem{Name: "Heineken 0,0 %", PriceCents: 600},
		MenuItem{Name: "Briska fläder 0,5 %"},
	)
	drink("Läsk & juice", false,
		MenuItem{Name: "Coca-Cola", PriceCents: 350},
		MenuItem{Name: "Coca-Cola Zero", PriceCents: 350},
		MenuItem{Name: "Jaffa", PriceCents: 350},
		MenuItem{Name: "Sprite Zero", PriceCents: 350},
		MenuItem{Name: "Grannas äppeljuice", PriceCents: 350},
	)
	drink("Kaffe & te", false,
		MenuItem{Name: "Kaffe"},
		MenuItem{Name: "Te"},
		MenuItem{Name: "Espresso"},
		MenuItem{Name: "Espresso doppio"},
		MenuItem{Name: "Cappuccino"},
		MenuItem{Name: "Caffè latte"},
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
	"Laxfilé": true, "Svamprisotto": true, "Ozmo-burgare": true, "Chokladfondant": true,
	"Pannacotta": true, "Sorbet": true, "Läsk 33 cl": true, "Mineralvatten": true,
	"Kaffe": true, "Husets vin, glas": true,
}

// upgradeMenu puts in the restaurant's menu, once, if the data file still has
// the made-up demo menu (and nothing else). A menu the manager has built is
// never touched. Callers hold s.mu.
func (s *Store) upgradeMenu() bool {
	if s.d.MenuVersion >= menuVersion {
		return false
	}
	s.d.MenuVersion = menuVersion
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
