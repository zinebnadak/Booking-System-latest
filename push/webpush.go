// Package push sends Web Push notifications (RFC 8030) to staff phones,
// encrypted for the phone (RFC 8291) and signed by this server (VAPID,
// RFC 8292). It uses only Go's standard library and no paid service: the
// phone's own push service (Apple, Google, Mozilla) delivers it.
package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Subscription is what the phone's browser gives when it agrees to
// notifications.
type Subscription struct {
	Endpoint string
	P256dh   string // base64url
	Auth     string // base64url
}

// ErrGone: the phone's subscription has ended; forget it.
var ErrGone = errors.New("subscription expired or removed")

// NewKey makes a new private key for this server (PKCS#8, base64).
func NewKey() (string, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

// Sender sends notifications with this server's key.
type Sender struct {
	key     *ecdsa.PrivateKey
	pub     []byte // uncompressed P-256 point, 65 bytes
	Subject string // "mailto:…" or the site's address: who to contact about these pushes
	Client  *http.Client
}

// NewSender loads the server's key (from NewKey).
func NewSender(keyB64, subject string) (*Sender, error) {
	der, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		return nil, err
	}
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	ek, ok := k.(*ecdsa.PrivateKey)
	if !ok || ek.Curve != elliptic.P256() {
		return nil, errors.New("push key: not a P-256 key")
	}
	pk, err := ek.PublicKey.ECDH()
	if err != nil {
		return nil, err
	}
	return &Sender{key: ek, pub: pk.Bytes(), Subject: subject, Client: &http.Client{Timeout: 15 * time.Second}}, nil
}

// PublicKey is the key the phone's browser needs to subscribe
// (applicationServerKey), base64url.
func (s *Sender) PublicKey() string { return base64.RawURLEncoding.EncodeToString(s.pub) }

// Send delivers payload (at most about 3 KB) to one phone. ErrGone means the
// subscription is dead and should be removed.
func (s *Sender) Send(ctx context.Context, sub Subscription, payload []byte, ttl time.Duration) error {
	u, err := url.Parse(sub.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("push endpoint: %q isn't an https address", sub.Endpoint)
	}
	uaPub, err := b64(sub.P256dh)
	if err != nil {
		return fmt.Errorf("push p256dh: %w", err)
	}
	auth, err := b64(sub.Auth)
	if err != nil {
		return fmt.Errorf("push auth: %w", err)
	}
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	body, err := Encrypt(payload, uaPub, auth, asPriv, salt)
	if err != nil {
		return err
	}
	jwt, err := s.vapidJWT(u.Scheme+"://"+u.Host, time.Now().Add(12*time.Hour))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", fmt.Sprint(int(ttl.Seconds())))
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", "vapid t="+jwt+", k="+s.PublicKey())
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	case resp.StatusCode >= 300:
		return fmt.Errorf("push service said %s", resp.Status)
	}
	return nil
}

// Encrypt makes the message body for one phone (RFC 8291, aes128gcm with a
// single record). asPriv and salt must be new for every message.
func Encrypt(plaintext, uaPub, authSecret []byte, asPriv *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(salt) != 16 || len(authSecret) == 0 {
		return nil, errors.New("push: bad salt or auth secret")
	}
	ua, err := ecdh.P256().NewPublicKey(uaPub)
	if err != nil {
		return nil, fmt.Errorf("push: phone key: %w", err)
	}
	shared, err := asPriv.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := asPriv.PublicKey().Bytes()
	// IKM = HKDF(auth secret, shared secret, "WebPush: info" || 0 || ua_public || as_public, 32)
	info := append(append([]byte("WebPush: info\x00"), uaPub...), asPub...)
	ikm := hkdf(authSecret, shared, info, 32)
	cek := hkdf(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	const rs = 4096
	if len(plaintext)+1+gcm.Overhead() > rs {
		return nil, errors.New("push: message too long")
	}
	record := append(append([]byte(nil), plaintext...), 0x02) // 0x02: the last (only) record
	head := make([]byte, 0, 16+4+1+len(asPub))
	head = append(head, salt...)
	head = binary.BigEndian.AppendUint32(head, rs)
	head = append(head, byte(len(asPub)))
	head = append(head, asPub...)
	return gcm.Seal(head, nonce, record, nil), nil
}

// hkdf is HKDF-SHA256 (RFC 5869) for outputs up to 32 bytes.
func hkdf(salt, ikm, info []byte, n int) []byte {
	m := hmac.New(sha256.New, salt)
	m.Write(ikm)
	prk := m.Sum(nil)
	m = hmac.New(sha256.New, prk)
	m.Write(info)
	m.Write([]byte{1})
	return m.Sum(nil)[:n]
}

// vapidJWT signs "this server sends to aud until exp" (ES256).
func (s *Sender) vapidJWT(aud string, exp time.Time) (string, error) {
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{"aud": aud, "exp": exp.Unix(), "sub": s.Subject})
	if err != nil {
		return "", err
	}
	signing := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, sg, err := ecdsa.Sign(rand.Reader, s.key, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	sg.FillBytes(sig[32:])
	return signing + "." + enc.EncodeToString(sig), nil
}

// b64 reads base64url with or without padding (browsers send either).
func b64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	return base64.RawURLEncoding.DecodeString(s)
}

// verifyJWT checks a token's signature (for tests).
func verifyJWT(pub *ecdsa.PublicKey, tok string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return false
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	return ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}
