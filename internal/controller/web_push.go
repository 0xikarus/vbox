package controller

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
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"time"

	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/secrets"
)

// Web Push (RFC 8030) with VAPID (RFC 8292) and aes128gcm payloads
// (RFC 8291 + RFC 8188), implemented with the standard library only. The
// controller holds one VAPID keypair; each browser subscription stores its
// endpoint and message keys per account.

const (
	webPushTTLSeconds       = 86400
	webPushRecordSize       = 4096
	webPushMaxPayload       = 2000
	webPushNotificationText = 140
)

var errPushSubscriptionGone = errors.New("push subscription is gone")

type vapidKeyPair struct {
	Public  string
	Private string
}

type pushSubscription struct {
	ID        string
	AccountID string
	UserID    string
	Endpoint  string
	P256DH    string
	Auth      string
	UserAgent string
}

func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func base64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func generateVapidKeyPair() (vapidKeyPair, error) {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return vapidKeyPair{}, err
	}
	return vapidKeyPair{Public: base64url(key.PublicKey().Bytes()), Private: base64url(key.Bytes())}, nil
}

// EnsureVapidKeys returns the controller-wide VAPID keypair, generating and
// sealing a fresh pair on first use. The private key never leaves the sealed
// column at rest.
func (s *Store) EnsureVapidKeys(ctx context.Context, envelope *secrets.Envelope) (vapidKeyPair, error) {
	var pair vapidKeyPair
	var sealed string
	err := s.DB.QueryRowContext(ctx, `SELECT vapid_public,vapid_private FROM web_push_settings WHERE id=true`).Scan(&pair.Public, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		generated, genErr := generateVapidKeyPair()
		if genErr != nil {
			return pair, genErr
		}
		if envelope == nil {
			return pair, fmt.Errorf("web push requires the controller secret envelope")
		}
		sealed, err = envelope.Seal("web-push-vapid", []byte(generated.Private))
		if err != nil {
			return pair, err
		}
		if _, err = s.DB.ExecContext(ctx, `INSERT INTO web_push_settings(id,vapid_public,vapid_private) VALUES(true,$1,$2) ON CONFLICT(id) DO NOTHING`, generated.Public, sealed); err != nil {
			return pair, err
		}
		err = s.DB.QueryRowContext(ctx, `SELECT vapid_public,vapid_private FROM web_push_settings WHERE id=true`).Scan(&pair.Public, &sealed)
	}
	if err != nil {
		return pair, err
	}
	if envelope == nil {
		return pair, fmt.Errorf("web push requires the controller secret envelope")
	}
	plain, err := envelope.Open("web-push-vapid", sealed)
	if err != nil {
		return pair, err
	}
	pair.Private = string(plain)
	return pair, nil
}

func validatePushSubscription(req v1.PutPushSubscriptionRequest) error {
	endpoint, err := url.Parse(req.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return fmt.Errorf("endpoint must be an https URL")
	}
	if len(req.Endpoint) > 4096 {
		return fmt.Errorf("endpoint is too long")
	}
	p256dh, err := base64.RawURLEncoding.DecodeString(req.Keys.P256DH)
	if err != nil || len(p256dh) != 65 || p256dh[0] != 0x04 {
		return fmt.Errorf("p256dh must be an uncompressed P-256 point")
	}
	auth, err := base64.RawURLEncoding.DecodeString(req.Keys.Auth)
	if err != nil || len(auth) < 12 || len(auth) > 64 {
		return fmt.Errorf("auth secret must be 12-64 bytes")
	}
	return nil
}

func (s *Store) PutPushSubscription(ctx context.Context, p Principal, req v1.PutPushSubscriptionRequest) error {
	agent := req.UserAgent
	if len(agent) > 200 {
		agent = agent[:200]
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A browser endpoint belongs to one signed-in account. Reusing the same
	// Android WebAPK after account switching must not deliver both accounts' chat.
	if _, err := tx.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint=$1 AND (account_id<>$2 OR user_id<>$3)`, req.Endpoint, p.AccountID, p.UserID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO push_subscriptions(id,account_id,user_id,endpoint,p256dh,auth,user_agent)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT(account_id,endpoint) DO UPDATE
		SET user_id=EXCLUDED.user_id,p256dh=EXCLUDED.p256dh,auth=EXCLUDED.auth,user_agent=EXCLUDED.user_agent,updated_at=now()`,
		uuid(), p.AccountID, p.UserID, req.Endpoint, req.Keys.P256DH, req.Keys.Auth, agent)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeletePushSubscription(ctx context.Context, p Principal, endpoint string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE account_id=$1 AND user_id=$2 AND endpoint=$3`, p.AccountID, p.UserID, endpoint)
	return err
}

func (s *Store) deletePushSubscriptionByEndpoint(ctx context.Context, accountID, endpoint string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE account_id=$1 AND endpoint=$2`, accountID, endpoint)
	return err
}

func (s *Store) ListPushSubscriptions(ctx context.Context, accountID string) ([]pushSubscription, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id::text,account_id::text,user_id::text,endpoint,p256dh,auth,user_agent FROM push_subscriptions WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []pushSubscription
	for rows.Next() {
		var value pushSubscription
		if err := rows.Scan(&value.ID, &value.AccountID, &value.UserID, &value.Endpoint, &value.P256DH, &value.Auth, &value.UserAgent); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// vapidJWT signs the RFC 8292 Voluntary Application Server Identification
// token. sub is an https or mailto contact for the application server.
func vapidJWT(pair vapidKeyPair, audience, sub string, now time.Time) (string, error) {
	scalar, err := base64.RawURLEncoding.DecodeString(pair.Private)
	if err != nil {
		return "", err
	}
	x, y := elliptic.P256().ScalarBaseMult(scalar)
	key := &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, D: new(big.Int).SetBytes(scalar)}
	header := base64url([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{"aud": audience, "exp": now.Add(12 * time.Hour).Unix(), "sub": sub})
	if err != nil {
		return "", err
	}
	input := header + "." + base64url(claims)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return input + "." + base64url(signature), nil
}

// encryptWebPushPayload produces an aes128gcm content-coded body per
// RFC 8291 (ECDH + HKDF) and RFC 8188 (salt, record size, key id header).
func encryptWebPushPayload(plaintext []byte, p256dh, auth string) ([]byte, error) {
	uaPublic, err := base64.RawURLEncoding.DecodeString(p256dh)
	if err != nil {
		return nil, fmt.Errorf("decode subscriber public key: %w", err)
	}
	authSecret, err := base64.RawURLEncoding.DecodeString(auth)
	if err != nil {
		return nil, fmt.Errorf("decode subscriber auth secret: %w", err)
	}
	peer, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, fmt.Errorf("subscriber public key is not on P-256")
	}
	local, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := local.ECDH(peer)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	keyInfo := append([]byte("WebPush: info\x00"), uaPublic...)
	keyInfo = append(keyInfo, local.PublicKey().Bytes()...)
	ikm := hmacSHA256(hmacSHA256(authSecret, shared), append(keyInfo, 0x01))
	prk := hmacSHA256(salt, ikm)
	cek := hmacSHA256(prk, []byte("Content-Encoding: aes128gcm\x00\x01"))[:16]
	nonce := hmacSHA256(prk, []byte("Content-Encoding: nonce\x00\x01"))[:12]
	if len(plaintext)+17 > webPushRecordSize {
		return nil, fmt.Errorf("web push payload exceeds record size")
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	record := append(append([]byte{}, plaintext...), 0x02)
	ciphertext := aead.Seal(nil, nonce, record, nil)
	var body bytes.Buffer
	body.Write(salt)
	_ = binary.Write(&body, binary.BigEndian, uint32(webPushRecordSize))
	body.WriteByte(byte(len(local.PublicKey().Bytes())))
	body.Write(local.PublicKey().Bytes())
	body.Write(ciphertext)
	return body.Bytes(), nil
}

func webPushContact(publicURL string) string {
	value, err := url.Parse(publicURL)
	if err == nil && (value.Scheme == "https" || value.Scheme == "http") && value.Host != "" {
		return value.Scheme + "://" + value.Host
	}
	return "mailto:vmbox@localhost"
}

func (s *Server) sendWebPush(ctx context.Context, pair vapidKeyPair, sub pushSubscription, payload []byte) error {
	endpoint, err := url.Parse(sub.Endpoint)
	if err != nil {
		return err
	}
	body, err := encryptWebPushPayload(payload, sub.P256DH, sub.Auth)
	if err != nil {
		return err
	}
	token, err := vapidJWT(pair, endpoint.Scheme+"://"+endpoint.Host, webPushContact(s.PublicURL), time.Now().UTC())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Authorization", "vapid t="+token+", k="+pair.Public)
	req.Header.Set("TTL", fmt.Sprint(webPushTTLSeconds))
	req.Header.Set("Urgency", "normal")
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	response.Body.Close()
	switch {
	case response.StatusCode == 404 || response.StatusCode == 410:
		return errPushSubscriptionGone
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return nil
	default:
		return fmt.Errorf("push service returned status %d", response.StatusCode)
	}
}

// pushAgentReply notifies every registered browser of the account about a
// completed agent reply. It is fire-and-forget: push failures never block
// message delivery, and stale subscriptions are pruned on 404/410.
func (s *Server) pushAgentReply(ctx context.Context, accountID string, task v1.BoxTask, text string) {
	if s.Store == nil || s.Store.DB == nil || text == "" {
		return
	}
	muted, err := s.isBoxPushMuted(ctx, accountID, task.LogicalBoxID)
	if err != nil {
		s.Logger.Warn("could not check chat mute before push", "error", err)
		return
	}
	if muted {
		return
	}
	probe := v1.BoxMessage{Text: text}
	decodeBoxMessageQuestion(&probe)
	decodeBoxMessageCaptcha(&probe)
	decodeBoxMessageMail(&probe)
	decodeBoxMessageControl(&probe)
	s.pushAccountNotification(accountID, map[string]string{"title": task.BoxName, "body": probe.Text, "box": task.LogicalBoxID, "url": "/chat#box=" + task.LogicalBoxID})
}

func (s *Server) pushAccountNotification(accountID string, value map[string]string) {
	if s.Store == nil || s.Store.DB == nil {
		return
	}
	notice := []rune(value["body"])
	if len(notice) > webPushNotificationText {
		notice = append(append([]rune{}, notice[:webPushNotificationText-1]...), '…')
	}
	value["body"] = string(notice)
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > webPushMaxPayload {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		subs, err := s.Store.ListPushSubscriptions(ctx, accountID)
		if err != nil || len(subs) == 0 {
			return
		}
		pair, err := s.Store.EnsureVapidKeys(ctx, s.Store.Envelope)
		if err != nil {
			s.Logger.Warn("web push keys unavailable", "error", err)
			return
		}
		for _, sub := range subs {
			if len(payload)+17 > webPushRecordSize {
				break
			}
			err := s.sendWebPush(ctx, pair, sub, payload)
			if errors.Is(err, errPushSubscriptionGone) {
				_ = s.Store.deletePushSubscriptionByEndpoint(ctx, accountID, sub.Endpoint)
				continue
			}
			if err != nil {
				s.Logger.Warn("web push delivery failed", "endpoint", sub.Endpoint, "error", err)
			}
		}
	}()
}

func (s *Server) vapidKeyHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	pair, err := s.Store.EnsureVapidKeys(r.Context(), s.Store.Envelope)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, v1.VapidPublicKey{PublicKey: pair.Public})
}

func (s *Server) putPushSubscriptionHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.PutPushSubscriptionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validatePushSubscription(request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Store.PutPushSubscription(r.Context(), p, request); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) deletePushSubscriptionHandler(w http.ResponseWriter, r *http.Request, p Principal) {
	var request v1.DeletePushSubscriptionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if request.Endpoint == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("endpoint is required"))
		return
	}
	if err := s.Store.DeletePushSubscription(r.Context(), p, request.Endpoint); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
