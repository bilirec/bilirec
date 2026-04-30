package notify

type WebPushPublicKeyResponse struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key,omitempty"`
}

type WebPushUnsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

type WebPushSubscriptionRequest struct {
	Endpoint string      `json:"endpoint"`
	Keys     WebPushKeys `json:"keys"`
}

type WebPushKeys struct {
	Auth   string `json:"auth"`
	P256dh string `json:"p256dh"`
}
