package v1

import "time"

// Web Push (RFC 8030) subscription registration from a browser client.
type PutPushSubscriptionRequest struct {
	Endpoint  string     `json:"endpoint"`
	Keys      PushKeySet `json:"keys"`
	UserAgent string     `json:"userAgent,omitempty"`
}

type PushKeySet struct {
	P256DH string `json:"p256dh"`
	Auth   string `json:"auth"`
}

type DeletePushSubscriptionRequest struct {
	Endpoint string `json:"endpoint"`
}

type PushSubscription struct {
	ID        string    `json:"id"`
	Endpoint  string    `json:"endpoint"`
	UserAgent string    `json:"userAgent,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type VapidPublicKey struct {
	PublicKey string `json:"publicKey"`
}
