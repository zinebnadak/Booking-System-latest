package db

// DemoMenu is the small made-up menu the first versions started with. The
// tests use it (they were written against it); the restaurant's real menu
// is seedMenu.
func DemoMenu() []MenuItem {
	const (
		starter = "Förrätter"
		main    = "Huvudrätter"
		dessert = "Desserter"
		drinks  = "Drycker"
	)
	items := []MenuItem{
		{Category: starter, Name: "Toast skagen", Description: "Med räkor, dill & citron", PriceCents: 1450, Tags: "L, *G", Course: CourseStarter},
		{Category: starter, Name: "Rödbetssallad", Description: "Med getost, valnötter & honung", PriceCents: 1150, Tags: "V, G", Course: CourseStarter},
		{Category: starter, Name: "Fisksoppa", Description: "Med saffran, aioli & surdegsbröd", PriceCents: 1550, Tags: "L, *G", Course: CourseStarter},

		{Category: main, Name: "Kycklinglårfilé", Description: "Med rostad potatis, citronsås & grillade grönsaker", PriceCents: 2390, Tags: "L, G", Course: CourseMain},
		{Category: main, Name: "Laxfilé", Description: "Med dillstuvad potatis & ärtpuré", PriceCents: 2690, Tags: "G", Course: CourseMain},
		{Category: main, Name: "Svamprisotto", Description: "Med parmesan & krispig grönkål", PriceCents: 2190, Tags: "V, G", Course: CourseMain},
		{Category: main, Name: "E-Meal-burgare", Description: "Med cheddar, picklad lök & pommes frites", PriceCents: 1990, Tags: "*L", Course: CourseMain},

		{Category: dessert, Name: "Chokladfondant", Description: "Med vaniljglass", PriceCents: 950, Tags: "V", Course: CourseDessert},
		{Category: dessert, Name: "Pannacotta", Description: "Med bärkompott", PriceCents: 850, Tags: "V, G", Course: CourseDessert},
		{Category: dessert, Name: "Sorbet", Description: "Två kulor, dagens smak", PriceCents: 750, Tags: "M, G, V", Course: CourseDessert},

		{Category: drinks, Name: "Läsk 33 cl", PriceCents: 350, Drink: true},
		{Category: drinks, Name: "Mineralvatten", PriceCents: 300, Drink: true},
		{Category: drinks, Name: "Kaffe", PriceCents: 300, Drink: true},
		{Category: drinks, Name: "Husets vin, glas", PriceCents: 850, Drink: true, Alcohol: true},
	}
	for i := range items {
		items[i].Available = true
		items[i].Sort = (i + 1) * 10
	}
	return items
}

// startMenu is what a new data file starts with.
var startMenu = seedMenu

// UseStartMenu swaps the menu new data files start with (for tests) and
// returns a function that puts the old one back.
func UseStartMenu(f func() []MenuItem) (restore func()) {
	old := startMenu
	startMenu = f
	return func() { startMenu = old }
}
