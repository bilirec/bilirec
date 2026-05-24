package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"
)

func (s *Service) WebPushServiceState() *ServiceState {
	state := s.state.Load()
	if state == nil {
		return &ServiceState{}
	}
	return &ServiceState{
		Enabled:    state.Enabled,
		Subscriber: state.Subscriber,
		PublicKey:  state.PublicKey,
	}
}

func (s *Service) AddWebPushSubscription(sub webpush.Subscription) error {
	if sub.Endpoint == "" || sub.Keys.Auth == "" || sub.Keys.P256dh == "" {
		return fmt.Errorf("无效的订阅")
	}

	payload, err := json.Marshal(sub)
	if err != nil {
		return fmt.Errorf("序列化订阅失败：%w", err)
	}

	if err := s.bucket.Put([]byte(sub.Endpoint), payload); err != nil {
		return err
	}

	return nil
}

func (s *Service) RemoveWebPushSubscription(endpoint string) error {
	if endpoint == "" {
		return fmt.Errorf("无效的订阅 endpoint")
	} else if err := s.bucket.Delete([]byte(endpoint)); err != nil {
		return err
	}
	return nil
}

func (s *Service) publishWebPushPayload(payload []byte) {
	state := s.state.Load()
	if state == nil || !state.Enabled {
		return
	}

	staleEndpoints := make([]string, 0, 8)

	_ = s.bucket.ForEach(func(k, v []byte) error {
		var sub webpush.Subscription
		if err := json.Unmarshal(v, &sub); err != nil {
			staleEndpoints = append(staleEndpoints, string(k))
			return nil
		}

		if sub.Endpoint == "" || sub.Keys.Auth == "" || sub.Keys.P256dh == "" {
			staleEndpoints = append(staleEndpoints, string(k))
			return nil
		}

		options := &webpush.Options{
			HTTPClient:      s.httpClient,
			Subscriber:      state.Subscriber,
			VAPIDPublicKey:  state.PublicKey,
			VAPIDPrivateKey: state.privateKey,
			TTL:             7200, // 2 hours
		}

		resp, sendErr := webpush.SendNotification(payload, &sub, options)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}

		if resp != nil && (resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound) {
			staleEndpoints = append(staleEndpoints, sub.Endpoint)
		}

		if sendErr != nil {
			logger.Warnf("向 %s 发送 Web Push 通知失败：%v", sub.Endpoint, sendErr)
		}

		return nil
	})

	for _, endpoint := range staleEndpoints {
		if err := s.bucket.Delete([]byte(endpoint)); err != nil {
			logger.Warnf("删除过期 Web Push 订阅失败：%s", endpoint)
		}
	}
}

func loadOrCreateVAPIDKeys(secretDir string) (string, string, error) {
	publicPath := filepath.Join(secretDir, webPushPublicKeyFileName)
	privatePath := filepath.Join(secretDir, webPushPrivateKeyFileName)

	publicBytes, publicErr := os.ReadFile(publicPath)
	privateBytes, privateErr := os.ReadFile(privatePath)

	publicKey := strings.TrimSpace(string(publicBytes))
	privateKey := strings.TrimSpace(string(privateBytes))
	if publicErr == nil && privateErr == nil && publicKey != "" && privateKey != "" {
		return publicKey, privateKey, nil
	}

	generatedPrivateKey, generatedPublicKey, err := webpush.GenerateVAPIDKeys()
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
