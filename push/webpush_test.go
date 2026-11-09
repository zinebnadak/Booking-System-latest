package push

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The example in RFC 8291, appendix A: same keys and salt, same bytes.
func TestEncryptRFC8291Example(t *testing.T) {
	asPriv, err := ecdh.P256().NewPrivateKey(mustB64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	uaPub := mustB64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	auth := mustB64(t, "BTBZMqHH6r4Tts7J_aSIgg")
	salt := mustB64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	got, err := Encrypt([]byte("When I grow up, I want to be a watermelon"), uaPub, auth, asPriv, salt)
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if g := base64.RawURLEncoding.EncodeToString(got); g != want {
		t.Fatalf("got  %s\nwant %s", g, want)
	}
}

// decrypt is what the phone does (for the round-trip test).
func decrypt(t *testing.T, body []byte, uaPriv *ecdh.PrivateKey, auth []byte) []byte {
	t.Helper()
	salt, idlen := body[:16], int(body[20])
	asPubB := body[21 : 21+idlen]
	asPub, err := ecdh.P256().NewPublicKey(asPubB)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := uaPriv.ECDH(asPub)
	info := append(append([]byte("WebPush: info\x00"), uaPriv.PublicKey().Bytes()...), asPubB...)
	ikm := hkdf(auth, shared, info, 32)
	cek := hkdf(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	pt, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatal(err)
	}
	return pt[:len(pt)-1] // the 0x02 record delimiter
}

// A notification reaches the phone's push service encrypted for the phone
// and signed by the server; a dead subscription comes back as ErrGone.
func TestSend(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSender(key, "mailto:test@example.com")
	if err != nil {
		t.Fatal(err)
	}
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	var got []byte
	var authz string
	gone := false
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gone {
			w.WriteHeader(http.StatusGone)
			return
		}
		got, _ = io.ReadAll(r.Body)
		authz = r.Header.Get("Authorization")
		if r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") == "" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(201)
	}))
	defer srv.Close()
	s.Client = srv.Client()
	sub := Subscription{Endpoint: srv.URL + "/push/abc", P256dh: base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()), Auth: base64.URLEncoding.EncodeToString(auth)}
	msg := `{"title":"Bord 5","body":"Maten är klar"}`
	if err := s.Send(context.Background(), sub, []byte(msg), time.Hour); err != nil {
		t.Fatal(err)
	}
	if string(decrypt(t, got, ua, auth)) != msg {
		t.Fatal("the phone couldn't read it")
	}
	if !strings.HasPrefix(authz, "vapid t=") || !strings.Contains(authz, ", k="+s.PublicKey()) {
		t.Fatalf("authorization %q", authz)
	}
	tok := strings.TrimSuffix(strings.TrimPrefix(authz, "vapid t="), ", k="+s.PublicKey())
	if !verifyJWT(&s.key.PublicKey, tok) {
		t.Fatal("the VAPID signature doesn't check out")
	}
	gone = true
	if err := s.Send(context.Background(), sub, []byte(msg), time.Hour); err != ErrGone {
		t.Fatalf("dead subscription: %v", err)
	}
}
