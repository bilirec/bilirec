package webpush

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	swebpush "github.com/SherClockHolmes/webpush-go"
	"github.com/eric2788/bilirec/pkg/ds"
	"github.com/puzpuzpuz/xsync/v4"
)

const (
	PublicKeyFileName  = "_webpush_public_key"
	PrivateKeyFileName = "_webpush_private_key"
)

type Manager struct {
	subs       *xsync.Map[string, swebpush.Subscription]
	state      ds.Atomic[managerState]
	secretDir  string
	httpClient *http.Client
}

type managerState struct {
	enabled    bool
	subscriber string
	publicKey  string
	privateKey string
}

func NewManager(subscriber, secretDir string, httpClient *http.Client) *Manager {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	m := &Manager{
		subs:       xsync.NewMap[string, swebpush.Subscription](),
		secretDir:  secretDir,
		httpClient: httpClient,
	}
	m.state.Store(managerState{subscriber: subscriber})
	return m
}

func (m *Manager) Start() error {
	state, ok := m.state.Load()
	if !ok {
		state = managerState{subscriber: ""}
	}

	if strings.TrimSpace(state.subscriber) == "" {
		state.enabled = false
		state.publicKey = ""
		state.privateKey = ""
		m.state.Store(state)
		return nil
	}

	publicKey, privateKey, err := loadOrCreateVAPIDKeys(m.secretDir)
	if err != nil {
		return err
	}

	state.publicKey = publicKey
	state.privateKey = privateKey
	state.enabled = true
	m.state.Store(state)
	return nil
}

func (m *Manager) Enabled() bool {
	state, _ := m.state.Load()
	return state.enabled
}

func (m *Manager) PublicKey() string {
	state, _ := m.state.Load()
	return state.publicKey
}

func (m *Manager) AddSubscription(sub swebpush.Subscription) bool {
	if sub.Endpoint == "" || sub.Keys.Auth == "" || sub.Keys.P256dh == "" {
		return false
	}

	// Replace existing subscription for the same endpoint to keep only the latest keys.
	m.subs.Delete(sub.Endpoint)
	m.subs.Store(sub.Endpoint, sub)
	return true
}

func (m *Manager) RemoveSubscription(endpoint string) bool {
	if endpoint == "" {
		return false
	}

	if _, ok := m.subs.Load(endpoint); !ok {
		return false
	}
	m.subs.Delete(endpoint)
	return true
}

func (m *Manager) PublishJSON(v any) {
	payload, err := json.Marshal(v)
	if err != nil {
		return
	}
	m.publishPayload(payload)
}

func (m *Manager) publishPayload(payload []byte) {
	state, _ := m.state.Load()
	if !state.enabled {
		return
	}

	subs := make([]swebpush.Subscription, 0, 16)
	m.subs.Range(func(_ string, sub swebpush.Subscription) bool {
		subs = append(subs, sub)
		return true
	})

	options := &swebpush.Options{
		HTTPClient:      m.httpClient,
		Subscriber:      state.subscriber,
		VAPIDPublicKey:  state.publicKey,
		VAPIDPrivateKey: state.privateKey,
		TTL:             30,
	}

	for _, sub := range subs {
		resp, sendErr := swebpush.SendNotification(payload, &sub, options)
		if sendErr != nil {
			continue
		}

		if resp.Body != nil {
			_ = resp.Body.Close()
		}

		if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
			m.RemoveSubscription(sub.Endpoint)
		}
	}
}

func loadOrCreateVAPIDKeys(secretDir string) (string, string, error) {
	secretDir = strings.TrimSpace(secretDir)
	if secretDir == "" {
		return "", "", errors.New("secret directory is empty")
	}

	if err := os.MkdirAll(secretDir, 0700); err != nil {
		return "", "", err
	}

	publicPath := filepath.Join(secretDir, PublicKeyFileName)
	privatePath := filepath.Join(secretDir, PrivateKeyFileName)

	publicBytes, publicErr := os.ReadFile(publicPath)
	privateBytes, privateErr := os.ReadFile(privatePath)

	publicKey := strings.TrimSpace(string(publicBytes))
	privateKey := strings.TrimSpace(string(privateBytes))
	if publicErr == nil && privateErr == nil && publicKey != "" && privateKey != "" {
		return publicKey, privateKey, nil
	}

	generatedPrivateKey, generatedPublicKey, err := swebpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", err
	}

	if err := os.WriteFile(publicPath, []byte(generatedPublicKey), 0600); err != nil {
		return "", "", err
	}
	if err := os.WriteFile(privatePath, []byte(generatedPrivateKey), 0600); err != nil {
		return "", "", err
	}

	return generatedPublicKey, generatedPrivateKey, nil
}
