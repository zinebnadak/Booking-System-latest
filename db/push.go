package db

import (
	"errors"
	"time"
)

// ---------- Phones that get push notifications ----------

// PushSub is one phone (or tablet) that asked for notifications, and whose
// it is: a waiter hears about their tables' food, the chef about new orders.
type PushSub struct {
	Endpoint string    `json:"endpoint"` // the phone's push service address
	P256dh   string    `json:"p256dh"`   // the phone's key (base64url)
	Auth     string    `json:"auth"`     // the phone's secret (base64url)
	Role     string    `json:"role"`     // "waiter", "chef" or "manager"
	StaffID  int       `json:"staff_id,omitempty"`
	Lang     string    `json:"lang,omitempty"`
	Added    time.Time `json:"added"`
}

// MaxPushSubs is how many phones can get notifications.
const MaxPushSubs = 200

var ErrBadPushSub = errors.New("not a push subscription")

// AddPushSub saves a phone's subscription (again: it's updated).
func (s *Store) AddPushSub(p PushSub) error {
	if p.Endpoint == "" || p.P256dh == "" || p.Auth == "" || len(p.Endpoint) > 1000 || len(p.P256dh) > 200 || len(p.Auth) > 100 {
		return ErrBadPushSub
	}
	return s.changeOrders(func() (bool, error) {
		kept := make([]*PushSub, 0, len(s.d.PushSubs)+1) // a new slice: the old one stays for a failed save
		for _, o := range s.d.PushSubs {
			if o.Endpoint != p.Endpoint {
				kept = append(kept, o)
			}
		}
		if len(kept) >= MaxPushSubs {
			kept = kept[1:] // the oldest goes
		}
		s.d.PushSubs = append(kept, &p)
		return true, nil
	})
}

// RemovePushSub forgets a phone (it turned notifications off, or its
// subscription ran out).
func (s *Store) RemovePushSub(endpoint string) error {
	return s.changeOrders(func() (bool, error) {
		kept := make([]*PushSub, 0, len(s.d.PushSubs))
		for _, o := range s.d.PushSubs {
			if o.Endpoint != endpoint {
				kept = append(kept, o)
			}
		}
		changed := len(kept) != len(s.d.PushSubs)
		s.d.PushSubs = kept
		return changed, nil
	})
}

// PushSubs lists the phones that get notifications.
func (s *Store) PushSubs() []PushSub {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PushSub, 0, len(s.d.PushSubs))
	for _, p := range s.d.PushSubs {
		out = append(out, *p)
	}
	return out
}

// VAPIDKey is this server's private push key (PKCS#8, base64), made with
// make the first time and kept in the data file.
func (s *Store) VAPIDKey(make func() (string, error)) (string, error) {
	s.mu.Lock()
	k := s.d.Settings.VAPID
	s.mu.Unlock()
	if k != "" {
		return k, nil
	}
	nk, err := make()
	if err != nil {
		return "", err
	}
	err = s.changeOrders(func() (bool, error) {
		if s.d.Settings.VAPID != "" {
			nk = s.d.Settings.VAPID // made meanwhile
			return false, nil
		}
		s.d.Settings.VAPID = nk
		return true, nil
	})
	return nk, err
}
