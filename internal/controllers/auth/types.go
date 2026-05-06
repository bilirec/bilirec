package auth

// StatusResponse represents current authentication status
type StatusResponse struct {
	Authenticated bool         `json:"authenticated"` // Whether currently authenticated
	State         string       `json:"state"`         // Current auth state
	Account       *AccountInfo `json:"account,omitempty"`
	QR            *QRInfo      `json:"qr,omitempty"`
	LastError     string       `json:"lastError,omitempty"`
}

type AccountInfo struct {
	Mid   int    `json:"mid"`
	Uname string `json:"uname"`
}

type QRInfo struct {
	URL string `json:"url"`
}

type InitLoginResponse struct {
	QR    *QRInfo `json:"qr,omitempty"`
	Error string  `json:"error,omitempty"`
}
