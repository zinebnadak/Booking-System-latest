package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"emeal-booking/db"
	"emeal-booking/push"
)

// ---------- Push notifications on staff phones ----------
//
// A phone that turned notifications on (the bell on Orders and the kitchen
// screen) gets a message even when it's locked or the page is closed:
// the chef when a new order comes in, the waiter when their table's food is
// ready, everyone when a Wolt order is ready for the courier.

// pusher is the server's push sender, made on first use.
func (s *Server) pusher() (*push.Sender, error) {
	s.pushOnce.Do(func() {
		key, err := s.Store.VAPIDKey(push.NewKey)
		if err != nil {
			s.pushErr = err
			return
		}
		subject := "mailto:" + s.Cfg.RestaurantEmail
		if s.Cfg.RestaurantEmail == "" {
			subject = s.Cfg.BaseURL
		}
		s.pushSender, s.pushErr = push.NewSender(key, subject)
	})
	return s.pushSender, s.pushErr
}

// pushKey gives the phone this server's public key to subscribe with.
func (s *Server) pushKey(w http.ResponseWriter, r *http.Request) {
	p, err := s.pusher()
	if err != nil {
		http.Error(w, "push isn't available", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"key": p.PublicKey()})
}

type pushSubJSON struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// pushSubscribe saves the phone's subscription, with whose phone it is.
func (s *Server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	var in pushSubJSON
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || !strings.HasPrefix(in.Endpoint, "https://") {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	l := loginOf(r)
	role := string(l.Role)
	if rank(l.Role) >= rank(roleManager) {
		role = string(roleManager)
	}
	err := s.Store.AddPushSub(db.PushSub{Endpoint: in.Endpoint, P256dh: in.Keys.P256dh, Auth: in.Keys.Auth,
		Role: role, StaffID: l.StaffID, Lang: langOf(r), Added: s.Now()})
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var in pushSubJSON
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	s.Store.RemovePushSub(in.Endpoint)
	w.WriteHeader(http.StatusNoContent)
}

// pushMsg is what the phone shows. Title and Body are given per language.
type pushMsg struct {
	Title, Body func(lang string) string
	URL         string // opens when tapped
	Tag         string // a newer message with the same tag replaces the older one
}

// pushTo picks who gets a message.
type pushTo struct {
	Roles   []string // "waiter", "chef", "manager"
	StaffID int      // only this waiter (0: everyone in Roles)
}

func (t pushTo) wants(p db.PushSub) bool {
	if t.StaffID != 0 && p.Role == string(roleWaiter) {
		return p.StaffID == t.StaffID
	}
	for _, r := range t.Roles {
		if r == p.Role {
			return true
		}
	}
	return false
}

// notify sends a message to the matching phones in the background. Dead
// subscriptions are forgotten.
func (s *Server) notify(to pushTo, m pushMsg) {
	if s.pushOff {
		return
	}
	subs := s.Store.PushSubs()
	var send []db.PushSub
	for _, p := range subs {
		if to.wants(p) {
			send = append(send, p)
		}
	}
	if len(send) == 0 {
		return
	}
	p, err := s.pusher()
	if err != nil {
		log.Printf("push: %v", err)
		return
	}
	s.pushWG.Add(1)
	go func() {
		defer s.pushWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		for _, sub := range send {
			wg.Add(1)
			go func(sub db.PushSub) {
				defer wg.Done()
				lang := sub.Lang
				if lang == "" {
					lang = langSV
				}
				body, _ := json.Marshal(map[string]string{"title": m.Title(lang), "body": m.Body(lang), "url": m.URL, "tag": m.Tag})
				err := p.Send(ctx, push.Subscription{Endpoint: sub.Endpoint, P256dh: sub.P256dh, Auth: sub.Auth}, body, 2*time.Hour)
				switch {
				case errors.Is(err, push.ErrGone):
					s.Store.RemovePushSub(sub.Endpoint)
				case err != nil:
					log.Printf("push to %s: %v", hostOf(sub.Endpoint), err)
				}
			}(sub)
		}
		wg.Wait()
	}()
}

func hostOf(u string) string {
	u = strings.TrimPrefix(u, "https://")
	if i := strings.IndexByte(u, '/'); i >= 0 {
		return u[:i]
	}
	return u
}

// ticketItems: "2× Burger, 1× Salad".
func ticketItems(t db.Ticket) string {
	var parts []string
	for _, it := range t.Items {
		if !it.Cancelled {
			parts = append(parts, strconv.Itoa(it.Qty)+"× "+it.Name)
		}
	}
	return strings.Join(parts, ", ")
}

// placeOf: "Table 5" / "Bord 5", "Wolt · Lisa" or "Takeaway #12".
func placeOf(lang string, t db.Ticket) string {
	switch {
	case t.Wolt != "":
		return "Wolt · " + t.Wolt
	case t.Takeaway != "":
		return stf(lang, "Takeaway #%d", t.OrderNo)
	}
	return stf(lang, "Table %d", t.Table)
}

// notifyNewest tells the chef about the newest kitchen ticket that match
// picks (after a course or a meal was sent, or a takeaway order came in).
func (s *Server) notifyNewest(match func(db.Ticket) bool) {
	var best *db.Ticket
	for _, t := range s.Store.OpenTickets() {
		if match(t) && (best == nil || t.ID > best.ID) {
			t := t
			best = &t
		}
	}
	s.notifyNewTicket(best)
}

// notifyNewTicket: a new order for the kitchen (the chef's phone).
func (s *Server) notifyNewTicket(t *db.Ticket) {
	if t == nil || t.Bar || t.Held || len(t.Items) == 0 {
		return
	}
	tk := *t
	s.notify(pushTo{Roles: []string{string(roleChef)}}, pushMsg{
		Title: func(lang string) string { return st(lang, "New order") + " · " + placeOf(lang, tk) },
		Body:  func(string) string { return ticketItems(tk) },
		URL:   "/admin/kitchen", Tag: "ticket-" + strconv.Itoa(tk.ID),
	})
}

// notifyReady: the kitchen finished a ticket. A table's waiter hears it
// (everyone on the floor when the table has no waiter); a Wolt order goes
// to everyone on the floor.
func (s *Server) notifyReady(ticketID int) {
	tk, found := s.Store.TicketByID(ticketID)
	if !found || tk.Bar || tk.Table == 0 || tk.Message != "" {
		return
	}
	to := pushTo{Roles: []string{string(roleWaiter), string(roleManager)}}
	if b, err := s.Store.BillByID(tk.BillID); err == nil && b.Waiter != 0 && b.Wolt == "" {
		to.StaffID = b.Waiter
	}
	s.notify(to, pushMsg{
		Title: func(lang string) string {
			if tk.Wolt != "" {
				return placeOf(lang, tk) + ": " + st(lang, "ready for the courier")
			}
			return placeOf(lang, tk) + ": " + st(lang, "food ready")
		},
		Body: func(lang string) string { return ticketItems(tk) + " · " + st(lang, "Go to the kitchen") },
		URL:  "/admin/orders", Tag: "ready-" + strconv.Itoa(tk.ID),
	})
}

// serviceWorker is the script that shows notifications on staff phones.
// It lives at the top of the site so it may look after /admin/ pages.
func (s *Server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.static, "sw.js")
	if err != nil {
		s.notFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

// notifyLate: a guest said on their booking page that they're running late.
func (s *Server) notifyLate(b db.Booking) {
	s.notify(pushTo{Roles: []string{string(roleWaiter), string(roleManager)}}, pushMsg{
		Title: func(lang string) string {
			return b.Name + " · " + guestsCount(lang, b.PartySize) + ": " + stf(lang, "%d min late", b.LateMinutes)
		},
		Body: func(lang string) string {
			return stf(lang, "Table %d", b.TableNo) + " · " + b.Time + " · " + stf(lang, "table kept until %s", clockOf(b.NoShowTime()))
		},
		URL: "/admin/day?date=" + url.QueryEscape(b.Date) + "&hl=" + url.QueryEscape(b.Ref), Tag: "late-" + b.Ref,
	})
}
