package payments

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestWebhookSignature(t *testing.T) {
	payload := []byte(`{"id":"evt_1","type":"checkout.session.completed","data":{"object":{}}}`)
	now := time.Now()
	header := SignForTest(payload, "whsec_test", now)

	ev, err := ParseWebhook(payload, header, "whsec_test", now)
	if err != nil || ev.Type != "checkout.session.completed" {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if _, err := ParseWebhook(payload, header, "whsec_wrong", now); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if _, err := ParseWebhook([]byte(`{"tampered":true}`), header, "whsec_test", now); err == nil {
		t.Fatal("tampered payload accepted")
	}
	if _, err := ParseWebhook(payload, header, "whsec_test", now.Add(10*time.Minute)); err == nil {
		t.Fatal("old webhook accepted")
	}
}

func TestCreateCheckoutSession(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test_x" || r.URL.Path != "/v1/checkout/sessions" {
			http.Error(w, `{"error":{"message":"bad"}}`, 400)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		got, _ = url.ParseQuery(string(raw))
		w.Write([]byte(`{"id":"cs_1","url":"https://checkout.stripe.com/c/cs_1"}`))
	}))
	defer srv.Close()

	c := &Client{SecretKey: "sk_test_x", APIBase: srv.URL}
	s, err := c.CreateCheckoutSession(context.Background(), CheckoutParams{
		Reference: "abc", Description: "Deposit", AmountCents: 2000,
		CustomerEmail: "a@b.fi", SuccessURL: "http://x/ok", CancelURL: "http://x/no",
		ExpiresAt: time.Now().Add(31 * time.Minute),
	})
	if err != nil || s.URL == "" {
		t.Fatalf("create: %v", err)
	}
	if got.Get("line_items[0][price_data][unit_amount]") != "2000" || got.Get("client_reference_id") != "abc" {
		t.Fatalf("unexpected form: %v", got)
	}
}

func TestTablePaymentWithoutEmailAndExpire(t *testing.T) {
	var got url.Values
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path == "/v1/checkout/sessions" {
			got, _ = url.ParseQuery(string(raw))
			w.Write([]byte(`{"id":"cs_2","url":"https://checkout.stripe.com/c/cs_2","status":"open"}`))
			return
		}
		w.Write([]byte(`{"id":"cs_2","status":"expired"}`))
	}))
	defer srv.Close()

	c := &Client{SecretKey: "sk_test_x", APIBase: srv.URL}
	if _, err := c.CreateCheckoutSession(context.Background(), CheckoutParams{
		Reference: "tab-1", Description: "Table 5", AmountCents: 2340,
		SuccessURL: "http://x/ok", CancelURL: "http://x/no", ExpiresAt: time.Now().Add(31 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["customer_email"]; ok {
		t.Fatalf("an empty email was sent: %v", got)
	}
	s, err := c.ExpireCheckoutSession(context.Background(), "cs_2")
	if err != nil || s.Status != "expired" {
		t.Fatalf("expire: %+v %v", s, err)
	}
	if paths[1] != "POST /v1/checkout/sessions/cs_2/expire" {
		t.Fatalf("calls %v", paths)
	}
}
