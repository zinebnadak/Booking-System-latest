package handlers

// Swedish for the "Work in progress" notes on the parts of the manager's
// view that aren't finished yet. Kept apart so they're easy to find and
// remove once each part is done.
func init() {
	for en, sv := range wipSV {
		staffSV[en] = sv
	}
}

var wipSV = map[string]string{
	"Work in progress": "Under arbete",
	"What guests told you after their visit. It's private: nothing here is published.":                                                          "Det gästerna berättade efter sitt besök. Det är privat: inget här publiceras.",
	"Feedback isn't finished yet. Guests can already give stars and a comment, and they show up here. How it looks and works may still change.": "Omdömen är inte klara än. Gästerna kan redan ge stjärnor och en kommentar, och de syns här. Hur det ser ut och fungerar kan fortfarande ändras.",
	// Staff: shared till
	"How it will work": "Så här ska det fungera",
	"Shared till lets several waiters share one device, for example an iPad by the bar, the same way as the till today.": "Med den delade kassan kan flera servitörer dela en enhet, till exempel en iPad vid baren, på samma sätt som kassan i dag.",
	"You choose which device becomes the shared till, on this page.":                                                     "Du väljer på den här sidan vilken enhet som blir den delade kassan.",
	"A waiter walks up, types their number and a 4-digit PIN, and is logged in.":                                         "En servitör går fram, skriver sitt nummer och en fyrsiffrig PIN, och är inloggad.",
	"They take the order, then tap Lock, so the next person can log in.":                                                 "Hen tar beställningen och trycker sedan på Lås, så att nästa person kan logga in.",
	"The same keypad works as a time clock: waiters clock in and out of their shift there.":                              "Samma knappsats fungerar som stämpelklocka: servitörerna stämplar in och ut från sitt pass där.",
	"Why it's safe": "Varför det är säkert",
	"The keypad only works on devices you've made a till here, so nobody can try PINs from outside. Everything a waiter does is still recorded with their name.": "Knappsatsen fungerar bara på enheter som du har gjort till kassa här, så ingen utifrån kan prova PIN-koder. Allt en servitör gör sparas fortfarande med hens namn.",

	// Calendar
	"Coming soon: a private link you add once to your phone's calendar (Google Calendar or iPhone). Every confirmed booking then shows up there by itself.": "Kommer snart: en privat länk som du lägger till en gång i mobilens kalender (Google Kalender eller iPhone). Varje bekräftad bokning syns sedan där av sig själv.",

	// Closed days
	"One place for our opening times": "Ett ställe för våra öppettider",
	"Our opening times change a lot: holidays, private events, short weeks. The plan is to change them only here, and have everything else update by itself.": "Våra öppettider ändras ofta: helgdagar, privata tillställningar, korta veckor. Planen är att bara ändra dem här, och att allt annat uppdateras av sig självt.",
	"You mark a closed day or different hours here, with a short reason.":                                                                                     "Du markerar en stängd dag eller andra tider här, med en kort förklaring.",
	"Booking stops for that day at once.":                                                            "Bokningen stängs för den dagen direkt.",
	"The public website shows the new times by itself.":                                              "Den publika webbplatsen visar de nya tiderna av sig själv.",
	"The same change is posted to our social media and Google, so guests see it wherever they look.": "Samma ändring läggs ut på våra sociala medier och på Google, så att gästerna ser den var de än tittar.",

	// Guest site
	"What the guest site could be": "Vad gästsidan kan bli",
	"The restaurant's own page for guests, on the phone or the computer. It's hidden while we decide what it should do.": "Restaurangens egen sida för gäster, i mobilen eller på datorn. Den är dold medan vi bestämmer vad den ska göra.",
	"Ideas": "Idéer",
	"Book a table online, confirmed at once.":                            "Boka bord online, bekräftat direkt.",
	"Order takeaway and pick it up at the restaurant.":                   "Beställ avhämtning och hämta på restaurangen.",
	"Opening times and closed days, always up to date from Closed days.": "Öppettider och stängda dagar, alltid uppdaterade från Stängda dagar.",
	"The menu, with allergens.":                                          "Menyn, med allergener.",
	"Leave feedback after a visit.":                                      "Lämna omdöme efter ett besök.",

	// Orders: Wolt
	"Taking Wolt orders here isn't ready yet.": "Att ta emot Wolt-beställningar här är inte klart än.",
}
