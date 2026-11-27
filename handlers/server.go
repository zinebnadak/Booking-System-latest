// Package handlers is the website: every URL and what happens when it's visited.
package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"emeal-booking/db"
	"emeal-booking/mailer"
	"emeal-booking/payments"
	"emeal-booking/push"
)

// Config holds the settings that come from the .env file.
type Config struct {
	BaseURL         string // public address of the site, e.g. https://book.ozmo.ax
	RestaurantName  string
	RestaurantPhone string
	RestaurantEmail string
	Address         string // on receipts, e.g. "Torggatan 1, AX-22100 Mariehamn"
	BusinessID      string // FO-nummer / Y-tunnus, on receipts
	ManagerEmail    string // gets an email for every confirmed booking
	ReportEmail     string // gets the end-of-day summary (empty: ManagerEmail)
	GoogleReviewURL string // the restaurant's "write a review" link on Google (offered to every guest after feedback)
	ManagerPhone    string // for SMS notifications (phase 2)

	// Features that are built but switched off for now (the code stays):
	// TableButtons: the call button on each table (TABLE_BUTTONS=1).
	// TableQR: guests order and pay with the table's QR code (TABLE_QR=1).
	// With TableQR off there's one QR code: feedback.
	TableButtons bool
	TableQR      bool
	// ThreeMeals: the "3 meals" deal (3 rätter) on the order screen
	// (THREE_MEALS=1). Off: it's not offered and "3 rätter" isn't shown.
	ThreeMeals bool
	// TableDeposit: guests pay a deposit to book a table online
	// (TABLE_DEPOSIT=1). Off: booking online is free and confirmed at once.
	TableDeposit  bool
	AdminUser     string
	AdminPassword string
	OwnerUser     string // a second manager login (was the head manager)
	OwnerPassword string
	CalendarToken string // secret part of the manager's calendar-feed link
	SessionSecret string // signs the staff login cookie (random if empty)
	RetentionDays int    // guest details are removed from bookings older than this (0 = keep)
	TrustProxy    bool   // behind a proxy that sets X-Forwarded-For (for the rate limits)
}

// Server holds everything the pages need.
type Server struct {
	Cfg   Config
	Store *db.Store
	Pay   *payments.Client
	Mail  *mailer.Mailer
	Now   func() time.Time // replaceable in tests

	pages    map[string]map[string]*template.Template // language -> page -> template
	static   fs.FS
	assetVer string // changes when CSS/JS change, so browsers never use stale files
	secret   []byte // signs the staff login cookie
	csp      string // Content-Security-Policy header, see securityHeaders
	rl       *limiter
	live     *hub // tells open Tables screens about button presses

	// Push notifications to staff phones (see pushes.go).
	pushOnce   sync.Once
	pushSender *push.Sender
	pushErr    error
	pushWG     sync.WaitGroup // messages still being sent (tests wait for them)
	pushOff    bool           // tests that don't want any sent
}

// M is the data passed to a page template.
type M map[string]any

var pageNames = []string{
	"home", "book", "takeaway", "status", "devpay", "message",
	"admin_day", "admin_calendar", "admin_menu", "admin_closed",
	"owner_today", "owner_charts", "login",
	"change", "waitlist", "legal",
	"admin_tables", "owner_buttons", "demo_buttons",
	"admin_orders", "admin_order", "order_bill", "admin_kitchen", "order_history",
	"admin_split", "order_receipts", "receipt",
	"table_join", "guest_tab", "guest_tip", "dev_tab", "admin_offline", "qr_stickers", "review", "owner_feedback",
	"admin_staff", "admin_activity", "admin_giftcards", "giftcard_print", "till", "admin_hours", "admin_stock", "admin_tableplan", "admin_schedule", "my_schedule", "admin_guestsite",
}

// New builds the server. files must contain templates/ and static/.
func New(cfg Config, store *db.Store, pay *payments.Client, mail *mailer.Mailer, files fs.FS) (*Server, error) {
	s := &Server{Cfg: cfg, Store: store, Pay: pay, Mail: mail, Now: time.Now, pages: map[string]map[string]*template.Template{}, rl: newLimiter(), live: newHub()}
	store.Clock = func() time.Time { return s.Now() } // the store tells time like the server (demo clock, tests)
	if cfg.SessionSecret != "" {
		sum := sha256.Sum256([]byte(cfg.SessionSecret))
		s.secret = sum[:]
	} else {
		s.secret = newSecret()
	}
	// Every page is prepared once per language, so the staff pages can use
	// {{tr "Table bookings"}} and get "Bordsbokningar" in Swedish.
	for _, lang := range []string{langSV, langEN} {
		s.pages[lang] = map[string]*template.Template{}
		for _, name := range pageNames {
			t, err := template.New("").Funcs(funcsFor(lang, store.WoltName)).ParseFS(files, "templates/layout.html", "templates/partials.html", "templates/"+name+".html")
			if err != nil {
				return nil, err
			}
			s.pages[lang][name] = t
		}
	}
	static, err := fs.Sub(files, "static")
	if err != nil {
		return nil, err
	}
	s.static = static
	h := sha256.New()
	fs.WalkDir(static, ".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if b, err := fs.ReadFile(static, path); err == nil {
				h.Write(b)
			}
		}
		return nil
	})
	s.assetVer = hex.EncodeToString(h.Sum(nil))[:10]
	layout, err := fs.ReadFile(files, "templates/layout.html")
	if err != nil {
		return nil, err
	}
	s.csp = buildCSP(layout, cfg.BaseURL)
	return s, nil
}

// Routes connects every URL to its handler.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Guests
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /book", s.bookPage)
	mux.HandleFunc("GET /book/times", s.bookTimes)
	mux.HandleFunc("POST /book", s.limited("book", limitBooking, s.bookSubmit))
	mux.HandleFunc("GET /takeaway", s.takeawayPage)
	mux.HandleFunc("POST /takeaway", s.limited("book", limitBooking, s.takeawaySubmit))
	mux.HandleFunc("GET /b/{ref}", s.statusPage)
	mux.HandleFunc("GET /b/{ref}/calendar.ics", s.guestICS)
	mux.HandleFunc("POST /b/{ref}/cancel", s.limited("guest", limitGuestAct, s.guestCancel))
	mux.HandleFunc("POST /b/{ref}/late", s.limited("guest", limitGuestAct, s.guestLate))
	mux.HandleFunc("GET /b/{ref}/change", s.changePage)
	mux.HandleFunc("GET /b/{ref}/change/times", s.changeTimes)
	mux.HandleFunc("POST /b/{ref}/change", s.limited("guest", limitGuestAct, s.changeSubmit))
	mux.HandleFunc("GET /waitlist", s.waitPage)
	mux.HandleFunc("POST /waitlist", s.limited("wait", limitWaitlist, s.waitSubmit))
	mux.HandleFunc("GET /w/{ref}", s.waitStatus)
	mux.HandleFunc("POST /w/{ref}/remove", s.limited("guest", limitGuestAct, s.waitRemove))
	mux.HandleFunc("GET /terms", s.legalPage("terms"))
	mux.HandleFunc("GET /privacy", s.legalPage("privacy"))
	mux.HandleFunc("GET /manifest.webmanifest", s.manifest(false))
	mux.HandleFunc("GET /sw.js", s.serviceWorker)
	mux.HandleFunc("GET /staff.webmanifest", s.manifest(true))
	mux.HandleFunc("GET /dev/pay/{ref}", s.devPayPage)
	mux.HandleFunc("POST /dev/pay/{ref}", s.limited("guest", limitGuestAct, s.devPaySubmit))
	mux.HandleFunc("POST /stripe/webhook", s.stripeWebhook)
	mux.HandleFunc("GET /calendar/{token}", s.calendarFeed)
	// The buttons on the tables (they send their secret code, no login).
	mux.HandleFunc("POST /api/press", s.ifButtons(s.apiButton(true)))
	mux.HandleFunc("POST /api/checkin", s.ifButtons(s.apiButton(false)))
	mux.HandleFunc("GET /demo/buttons", s.ifButtons(s.demoButtons))
	// A guest's receipt, from the QR code (the long random code is the key).
	mux.HandleFunc("GET /r/{ref}", s.publicReceipt)
	mux.HandleFunc("POST /demo/buttons/{no}", s.ifButtons(s.demoPress))
	// Ordering and paying from the table's QR code (the long random codes are the keys).
	mux.HandleFunc("GET /t/{code}", s.ifTableQR(s.tablePage))
	mux.HandleFunc("POST /t/{code}", s.ifTableQR(s.limited("join", limitJoin, s.tableJoin)))
	mux.HandleFunc("POST /t/{code}/unlock", s.ifTableQR(s.tableUnlock))
	mux.HandleFunc("GET /g/{key}", s.guestPage)
	mux.HandleFunc("GET /g/{key}/events", s.limited("tab", limitGuestTab, s.guestEvents))
	mux.HandleFunc("POST /g/{key}/name", s.limited("tab", limitGuestTab, s.guestName))
	mux.HandleFunc("POST /g/{key}/order", s.ifTableQR(s.limited("tab", limitGuestTab, s.guestOrder)))
	mux.HandleFunc("GET /g/{key}/tip", s.guestTip)
	mux.HandleFunc("POST /g/{key}/pay", s.limited("tab", limitGuestTab, s.guestPay))
	mux.HandleFunc("POST /g/{key}/cancel", s.limited("tab", limitGuestTab, s.tabCancel))
	mux.HandleFunc("POST /g/{key}/receipt/email", s.limited("mail", limitBooking, s.guestReceiptEmail))
	mux.HandleFunc("POST /g/{key}/receipt/paper", s.limited("tab", limitGuestTab, s.guestReceiptPaper))
	mux.HandleFunc("POST /g/{key}/call", s.ifTableQR(s.limited("tab", limitGuestTab, s.guestCall)))
	mux.HandleFunc("GET /g/{key}/back", s.limited("tab", limitGuestTab, s.tabCancel)) // Stripe's "back" link
	mux.HandleFunc("GET /g/{key}/paid", s.guestPaid)                                  // Stripe's "done" link
	mux.HandleFunc("GET /dev/tab/{ref}", s.devTabPage)
	mux.HandleFunc("POST /dev/tab/{ref}", s.limited("guest", limitGuestAct, s.devTabSubmit))
	files := http.StripPrefix("/static/", http.FileServer(http.FS(s.static)))
	mux.Handle("GET /static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// File names carry ?v=<hash>, so browsers may keep them for a long time.
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	}))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /till", s.tillPage)
	mux.HandleFunc("POST /till", s.tillSubmit)
	mux.HandleFunc("POST /till/lock", s.tillLock)
	mux.HandleFunc("GET /photos/{name}", s.servePhoto)
	// Feedback (from the review QR code, the table page and receipts).
	mux.HandleFunc("GET /review", s.reviewPage)
	mux.HandleFunc("POST /review", s.limited("review", limitReview, s.reviewStars))
	mux.HandleFunc("GET /review/{ref}", s.reviewStep2)
	mux.HandleFunc("POST /review/{ref}", s.limited("review2", limitReview, s.reviewFinish))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	// Manager (password protected)
	admin := http.NewServeMux()
	admin.HandleFunc("GET /admin", func(w http.ResponseWriter, r *http.Request) {
		home := "/admin/orders" // Orders is the staff's home page
		if roleOf(r) == roleManager {
			home = "/owner" // and the money page the manager's
		}
		http.Redirect(w, r, home, http.StatusSeeOther)
	})
	admin.HandleFunc("GET /admin/day", s.adminDay)
	admin.HandleFunc("GET /admin/tables", s.adminTables)
	admin.HandleFunc("GET /admin/tables/events", s.tablesEvents)
	admin.HandleFunc("GET /admin/events", s.tablesEvents) // the same live feed, for every live screen
	admin.HandleFunc("GET /admin/orders", s.adminOrders)
	admin.HandleFunc("GET /admin/orders/{table}", s.adminOrder)
	admin.HandleFunc("GET /admin/orders/{table}/bill", s.orderBillView)
	admin.HandleFunc("GET /admin/orders/{table}/history", s.orderHistory)
	admin.HandleFunc("GET /admin/orders/history", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/admin/orders/"+strconv.Itoa(atoiDefault(r.URL.Query().Get("table"), 0))+"/history", http.StatusSeeOther)
	})
	admin.HandleFunc("GET /admin/orders/{table}/split", s.splitPage)
	admin.HandleFunc("POST /admin/orders/{table}/split/{action}", s.splitAction)
	admin.HandleFunc("GET /admin/orders/{table}/receipts", s.receiptsPage)
	admin.HandleFunc("POST /admin/wolt", s.newWolt)
	admin.HandleFunc("GET /admin/orders/go", s.orderJump)
	admin.HandleFunc("POST /admin/shift", s.staffShift)
	admin.HandleFunc("GET /admin/push/key", s.pushKey)
	admin.HandleFunc("POST /admin/push/subscribe", s.pushSubscribe)
	admin.HandleFunc("POST /admin/push/unsubscribe", s.pushUnsubscribe)
	admin.HandleFunc("POST /admin/orders/{table}/add", s.orderAdd)
	admin.HandleFunc("POST /admin/orders/{table}/lines/{line}/{action}", s.orderLine)
	admin.HandleFunc("POST /admin/orders/{table}/qr", s.ifTableQR(s.tableQR))
	admin.HandleFunc("POST /admin/orders/{table}/{action}", s.orderBillAction)
	admin.HandleFunc("GET /admin/tables/qr", s.qrStickers)
	admin.HandleFunc("GET /admin/offline", s.offlinePlan)
	admin.HandleFunc("GET /admin/guestsite", s.guestSiteNote)
	admin.HandleFunc("POST /admin/tables/qr/{no}/new", s.ifTableQR(s.managerOnly(s.qrNewCode)))
	admin.HandleFunc("POST /admin/receipts/{bill}/{guest}/given", s.receiptGiven)
	admin.HandleFunc("POST /admin/phone-orders/{id}/{action}", s.phoneOrderAction)
	admin.HandleFunc("POST /admin/phone-orders/check", s.ifTableQR(s.checkPhoneOrders))
	admin.HandleFunc("GET /admin/kitchen", s.adminKitchen)
	admin.HandleFunc("POST /admin/kitchen/{id}/{action}", s.kitchenAction)
	admin.HandleFunc("POST /admin/tables/{no}/{action}", s.tableAction)
	admin.HandleFunc("POST /admin/mealplans/{id}/remove", s.mealPlanRemove)
	admin.HandleFunc("GET /admin/calendar", s.adminCalendar)
	admin.HandleFunc("POST /admin/bookings/{ref}/cancel", s.adminCancel)
	admin.HandleFunc("POST /admin/bookings/{ref}/arrived", s.adminArrived)
	admin.HandleFunc("POST /admin/bookings/{ref}/table", s.adminMoveTable)
	admin.HandleFunc("POST /admin/bookings/{ref}/pickedup", s.adminPickedUp)
	admin.HandleFunc("POST /admin/bookings/new", s.adminNewBooking)
	admin.HandleFunc("POST /admin/waitlist/{ref}/remove", s.adminWaitRemove)
	admin.HandleFunc("GET /admin/menu", s.managerOnly(s.adminMenu))
	admin.HandleFunc("POST /admin/menu", s.managerOnly(s.adminMenuSave))
	admin.HandleFunc("POST /admin/menu/{id}/delete", s.managerOnly(s.adminMenuDelete))
	admin.HandleFunc("POST /admin/menu/{id}/available", s.managerOnly(s.adminMenuAvailable))
	admin.HandleFunc("POST /admin/menu/{id}/soldout", s.menuSoldOut)
	admin.HandleFunc("GET /admin/stock", s.adminStock)
	admin.HandleFunc("POST /admin/stock/{id}", s.adminStockSet)
	admin.HandleFunc("POST /admin/stock/ingredients", s.adminIngredientAdd)
	admin.HandleFunc("POST /admin/online/{ref}/seen", s.adminPaymentSeen)
	admin.HandleFunc("POST /admin/stock/ingredients/{id}", s.adminIngredientAction)
	admin.HandleFunc("POST /admin/menu/{id}/photo/{action}", s.managerOnly(s.menuPhoto))
	admin.HandleFunc("GET /admin/closed", s.managerOnly(s.adminClosed))
	admin.HandleFunc("GET /admin/staff", s.managerOnly(s.adminStaff))
	admin.HandleFunc("POST /admin/staff", s.managerOnly(s.adminStaffAdd))
	admin.HandleFunc("POST /admin/staff/till", s.managerOnly(s.adminTillDevice))
	admin.HandleFunc("POST /admin/staff/managerpin", s.managerOnly(s.adminManagerPIN))
	admin.HandleFunc("POST /admin/staff/businessid", s.managerOnly(s.adminBusinessID))
	admin.HandleFunc("GET /admin/tableplan", s.managerOnly(s.adminTablePlan))
	admin.HandleFunc("GET /admin/schedule", s.managerOnly(s.adminSchedule))
	admin.HandleFunc("POST /admin/schedule", s.managerOnly(s.adminScheduleAdd))
	admin.HandleFunc("POST /admin/schedule/copy", s.managerOnly(s.adminScheduleCopy))
	admin.HandleFunc("POST /admin/schedule/{id}/remove", s.managerOnly(s.adminScheduleRemove))
	admin.HandleFunc("GET /admin/myschedule", s.mySchedule)
	admin.HandleFunc("POST /admin/tableplan", s.managerOnly(s.adminTablePlanSave))
	admin.HandleFunc("GET /admin/hours", s.managerOnly(s.adminHours))
	admin.HandleFunc("POST /admin/hours/{id}", s.managerOnly(s.adminHoursFix))
	admin.HandleFunc("POST /admin/staff/{id}/{action}", s.managerOnly(s.adminStaffAction))
	admin.HandleFunc("GET /admin/activity", s.managerOnly(s.adminActivity))
	admin.HandleFunc("POST /admin/closed", s.managerOnly(s.adminClosedAdd))
	admin.HandleFunc("POST /admin/closed/{date}/delete", s.managerOnly(s.adminClosedDelete))
	admin.HandleFunc("POST /admin/rooms/online", s.managerOnly(s.adminRoomOnline))
	admin.HandleFunc("GET /admin/giftcards", s.adminGiftCards)
	admin.HandleFunc("POST /admin/giftcards", s.adminGiftCardSell)
	admin.HandleFunc("GET /admin/giftcards/{code}/print", s.adminGiftCardPrint)
	admin.HandleFunc("POST /admin/giftcards/{code}/void", s.managerOnly(s.adminGiftCardVoid))
	// Every staff login (waiters too) opens these; managerOnly ones need the manager.
	mux.Handle("/admin", s.requireRole(roleWaiter, admin))
	mux.Handle("/admin/", s.requireRole(roleWaiter, admin))

	// The manager's money pages (waiters can't open them)
	owner := http.NewServeMux()
	owner.HandleFunc("GET /owner", s.ownerToday)
	owner.HandleFunc("GET /owner/charts", s.ownerCharts)
	owner.HandleFunc("POST /owner/cash", s.ownerCash)
	owner.HandleFunc("POST /owner/woltpct", s.ownerWoltPct)
	owner.HandleFunc("GET /owner/reports", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/owner/charts", http.StatusSeeOther)
	})
	owner.HandleFunc("GET /owner/export.csv", s.ownerReportsCSV)
	owner.HandleFunc("GET /owner/summary", s.ownerSummary)
	owner.HandleFunc("GET /owner/feedback", s.ownerFeedback)
	owner.HandleFunc("GET /owner/buttons", s.ifButtons(s.ownerButtons))
	owner.HandleFunc("POST /owner/buttons/{no}/new", s.ifButtons(s.ownerNewCode))
	mux.Handle("/owner", s.requireRole(roleManager, owner))
	mux.Handle("/owner/", s.requireRole(roleManager, owner))

	return logRequests(s.securityHeaders(rememberLang(mux)))
}

// render fills a page template and sends it, in the visitor's language
// (Swedish or English, switched at the top of every page).
func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, status int, data M) {
	s.renderBlock(w, r, page, "layout", status, data)
}

// renderBlock renders one named part of a page, e.g. just the list of times
// that the booking page swaps in without reloading.
func (s *Server) renderBlock(w http.ResponseWriter, r *http.Request, page, block string, status int, data M) {
	if data == nil {
		data = M{}
	}
	lang := langOf(r)
	data["Lang"] = lang
	data["T"] = tr(lang)
	data["SwitchSV"] = switchURL(r, langSV)
	data["SwitchEN"] = switchURL(r, langEN)
	data["Cfg"] = s.Cfg
	data["DemoMode"] = !s.Pay.Enabled()
	data["V"] = s.assetVer
	if _, ok := data["Role"]; !ok {
		data["Role"] = string(roleOf(r))
	}
	data["Path"] = r.URL.Path
	data["TillDevice"] = s.isTill(r)
	if rank(roleOf(r)) >= rank(roleManager) {
		data["MgrWaiting"] = s.Store.WaitingForManager() // the red number next to Orders
	}
	if l := loginOf(r); l.Role == roleWaiter || l.Role == roleChef {
		data["StaffName"] = l.Name
		if l.StaffID != 0 && !s.isTill(r) { // the till has its own clock card
			data["ShiftAsk"] = true
			if sh, on := s.Store.ClockedIn(l.StaffID); on {
				data["ShiftSince"] = clockOf(sh.In)
			}
		}
	}
	for _, k := range []string{"Errors", "Form"} {
		if _, ok := data[k]; !ok {
			data[k] = map[string]string{}
		}
	}
	var buf bytes.Buffer
	if err := s.pages[lang][page].ExecuteTemplate(&buf, block, data); err != nil {
		log.Printf("template %s: %v", page, err)
		http.Error(w, "Something went wrong", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// message shows a simple page with a heading, text and a link.
// title, text and linkText are word keys (see i18n.go).
func (s *Server) message(w http.ResponseWriter, r *http.Request, status int, title, text, linkHref, linkText string) {
	t := tr(langOf(r))
	s.render(w, r, "message", status, M{"Title": t[title], "Heading": t[title], "Text": t[text], "LinkHref": linkHref, "LinkText": t[linkText]})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.message(w, r, http.StatusNotFound, "m_notfound_t", "m_notfound_x", "/", "m_back")
}

// ---------- Middleware ----------

// sameOrigin checks that a form was submitted from our own site.
func sameOrigin(r *http.Request) bool {
	for _, h := range []string{"Origin", "Referer"} {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}
		u, err := url.Parse(v)
		return err == nil && u.Host == r.Host
	}
	return true // non-browser clients send neither header
}

// inlineScript finds the two small scripts written into layout.html itself.
var inlineScript = regexp.MustCompile(`(?s)<script(?: type="speculationrules")?>(.*?)</script>`)

// buildCSP writes the Content-Security-Policy: the browser may only run our
// own script files plus the exact inline scripts in layout.html (by their
// fingerprint). If someone ever manages to slip HTML into a page, their
// script won't run, and forms can't be pointed at another site.
func buildCSP(layout []byte, baseURL string) string {
	scripts := "'self'"
	for _, m := range inlineScript.FindAllSubmatch(layout, -1) {
		sum := sha256.Sum256(m[1])
		scripts += " 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	}
	// Forms post to this site, which may then send the guest on to Stripe.
	forms := "'self' https://checkout.stripe.com"
	if u, err := url.Parse(baseURL); err == nil && u.Scheme != "" && u.Host != "" {
		forms += " " + u.Scheme + "://" + u.Host
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + scripts,
		"style-src 'self' 'unsafe-inline'",
		"font-src 'self'",
		"img-src 'self' data:",
		"connect-src 'self'",
		"manifest-src 'self'",
		"worker-src 'self'",
		"form-action " + forms,
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"object-src 'none'",
	}, "; ")
}

// securityHeaders tells browsers to be strict with this site.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	https := strings.HasPrefix(s.Cfg.BaseURL, "https://")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", s.csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), browsing-topics=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if https {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		// Pages with a guest's details (their booking link, the demo payment
		// page, the login page) are never stored by the browser or a proxy.
		p := r.URL.Path
		if strings.HasPrefix(p, "/b/") || strings.HasPrefix(p, "/w/") || strings.HasPrefix(p, "/dev/") ||
			strings.HasPrefix(p, "/calendar/") || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/demo/") || strings.HasPrefix(p, "/r/") ||
			strings.HasPrefix(p, "/t/") || strings.HasPrefix(p, "/g/") || strings.HasPrefix(p, "/review/") || p == "/login" {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the real connection (the live
// Tables screen needs to flush events and keep its request open).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, logPath(r.URL.Path), rec.status, time.Since(start).Round(time.Millisecond))
	})
}

// logPath hides the secret part of the calendar-feed link in the log.
func logPath(p string) string {
	if strings.HasPrefix(p, "/calendar/") {
		return "/calendar/…"
	}
	// A guest's key at a table is what lets them order and pay: keep it out of logs.
	for _, pre := range []string{"/g/", "/t/"} {
		if rest, ok := strings.CutPrefix(p, pre); ok {
			if i := strings.IndexByte(rest, '/'); i >= 0 {
				return pre + "…" + rest[i:]
			}
			return pre + "…"
		}
	}
	return p
}

// ifButtons: the table buttons are switched off for now (Cfg.TableButtons):
// their pages and the API answer "not found". The code stays.
func (s *Server) ifButtons(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.Cfg.TableButtons {
			s.notFound(w, r)
			return
		}
		h(w, r)
	}
}

// ifTableQR: ordering with the table's QR code is switched off for now
// (Cfg.TableQR). An old table sticker leads to the feedback page instead;
// the rest answers "not found". The code stays.
func (s *Server) ifTableQR(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.Cfg.TableQR {
			if strings.HasPrefix(r.URL.Path, "/t/") {
				http.Redirect(w, r, "/review", http.StatusSeeOther)
				return
			}
			s.notFound(w, r)
			return
		}
		h(w, r)
	}
}
