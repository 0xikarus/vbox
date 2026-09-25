package controller

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/DATA-DOG/go-sqlmock"
)

func TestPushSubscriptionMovesToCurrentAccount(t *testing.T) {
	store, mock := testStore(t)
	p := Principal{AccountID: "current-account", UserID: "current-user"}
	request := v1.PutPushSubscriptionRequest{Endpoint: "https://push.example/sub/123", Keys: v1.PushKeySet{P256DH: "public", Auth: "auth"}, UserAgent: "Android Chrome"}
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM push_subscriptions WHERE endpoint").WithArgs(request.Endpoint, p.AccountID, p.UserID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO push_subscriptions").WithArgs(sqlmock.AnyArg(), p.AccountID, p.UserID, request.Endpoint, request.Keys.P256DH, request.Keys.Auth, request.UserAgent).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := store.PutPushSubscription(context.Background(), p, request); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// subscriberKeypair simulates a browser subscription key pair and lets the
// test decrypt a payload the way a push service client would (RFC 8291).
type subscriberKeypair struct {
	key  *ecdh.PrivateKey
	auth []byte
}

func newSubscriber(t *testing.T) (subscriberKeypair, string, string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	return subscriberKeypair{key: key, auth: auth}, base64url(key.PublicKey().Bytes()), base64url(auth)
}

func (s subscriberKeypair) decrypt(t *testing.T, body []byte) []byte {
	t.Helper()
	if len(body) < 16+4+1+65 {
		t.Fatalf("aes128gcm body too short: %d", len(body))
	}
	salt, record, keyIDLen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if record < 17 {
		t.Fatalf("record size %d too small", record)
	}
	serverPublic := body[21 : 21+keyIDLen]
	peer, err := ecdh.P256().NewPublicKey(serverPublic)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := s.key.ECDH(peer)
	if err != nil {
		t.Fatal(err)
	}
	keyInfo := append([]byte("WebPush: info\x00"), s.key.PublicKey().Bytes()...)
	keyInfo = append(keyInfo, serverPublic...)
	ikm := hmacSHA256(hmacSHA256(s.auth, shared), append(keyInfo, 0x01))
	prk := hmacSHA256(salt, ikm)
	cek := hmacSHA256(prk, []byte("Content-Encoding: aes128gcm\x00\x01"))[:16]
	nonce := hmacSHA256(prk, []byte("Content-Encoding: nonce\x00\x01"))[:12]
	block, err := aes.NewCipher(cek)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	record_text, err := aead.Open(nil, nonce, body[21+keyIDLen:], nil)
	if err != nil {
		t.Fatalf("subscriber could not decrypt payload: %v", err)
	}
	return record_text
}

func TestWebPushPayloadRoundTrip(t *testing.T) {
	subscriber, p256dh, auth := newSubscriber(t)
	plaintext := []byte(`{"title":"build-box","body":"Done — see the attached result","box":"abc"}`)
	body, err := encryptWebPushPayload(plaintext, p256dh, auth)
	if err != nil {
		t.Fatal(err)
	}
	decrypted := subscriber.decrypt(t, body)
	if len(decrypted) != len(plaintext)+1 {
		t.Fatalf("decrypted record length %d, want %d", len(decrypted), len(plaintext)+1)
	}
	if decrypted[len(decrypted)-1] != 0x02 {
		t.Fatalf("missing aes128gcm final-record delimiter: %x", decrypted[len(decrypted)-1])
	}
	if string(decrypted[:len(decrypted)-1]) != string(plaintext) {
		t.Fatalf("decrypted payload mismatch: %q", decrypted)
	}
}

func TestWebPushPayloadRejectsOversize(t *testing.T) {
	_, p256dh, auth := newSubscriber(t)
	if _, err := encryptWebPushPayload(make([]byte, webPushRecordSize), p256dh, auth); err == nil {
		t.Fatal("oversize payload must be rejected")
	}
}

func TestVapidJWT(t *testing.T) {
	pair, err := generateVapidKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	token, err := vapidJWT(pair, "https://push.example", "https://controller.example", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("JWT must have three segments, got %d", len(parts))
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || !strings.Contains(string(header), "ES256") {
		t.Fatalf("unexpected header %q", header)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		t.Fatalf("claims not decodable: %q", payload)
	}
	if claims.Aud != "https://push.example" || claims.Sub != "https://controller.example" {
		t.Fatalf("unexpected claims %+v", claims)
	}
	if claims.Exp <= time.Unix(1700000000, 0).Unix() || claims.Exp > time.Unix(1700000000, 0).Add(24*time.Hour).Unix() {
		t.Fatalf("exp out of expected window: %d", claims.Exp)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		t.Fatalf("ES256 signature must be 64 bytes, got %d", len(signature))
	}
	public, err := base64.RawURLEncoding.DecodeString(pair.Public)
	if err != nil || len(public) != 65 || public[0] != 0x04 {
		t.Fatalf("public key must be an uncompressed point, got %d bytes", len(public))
	}
	key := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(public[1:33]), Y: new(big.Int).SetBytes(public[33:])}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(signature[:32])
	s := new(big.Int).SetBytes(signature[32:])
	if !ecdsa.Verify(key, digest[:], r, s) {
		t.Fatal("VAPID JWT signature does not verify against the public key")
	}
}

func TestValidatePushSubscription(t *testing.T) {
	_, p256dh, auth := newSubscriber(t)
	valid := v1.PutPushSubscriptionRequest{Endpoint: "https://push.example/sub/123", Keys: v1.PushKeySet{P256DH: p256dh, Auth: auth}}
	if err := validatePushSubscription(valid); err != nil {
		t.Fatalf("valid subscription rejected: %v", err)
	}
	cases := []v1.PutPushSubscriptionRequest{
		{Endpoint: "http://push.example/sub", Keys: v1.PushKeySet{P256DH: p256dh, Auth: auth}},
		{Endpoint: "https://push.example/sub", Keys: v1.PushKeySet{P256DH: "!!!", Auth: auth}},
		{Endpoint: "https://push.example/sub", Keys: v1.PushKeySet{P256DH: p256dh, Auth: "c2hvcnQ"}},
		{Endpoint: strings.Repeat("https://push.example/", 300), Keys: v1.PushKeySet{P256DH: p256dh, Auth: auth}},
	}
	for index, value := range cases {
		if err := validatePushSubscription(value); err == nil {
			t.Fatalf("case %d: invalid subscription accepted", index)
		}
	}
}

func TestWebPushContact(t *testing.T) {
	if got := webPushContact("https://controller.example/api"); got != "https://controller.example" {
		t.Fatalf("contact must strip the path, got %q", got)
	}
	if got := webPushContact(""); got != "mailto:vmbox@localhost" {
		t.Fatalf("empty public URL must fall back, got %q", got)
	}
}
