package notify

import (
	"crypto/subtle"
	"strings"
)

func (s *Service) SubscribeSSE(token string) (uint64, <-chan []byte, error) {
	if strings.TrimSpace(s.sseToken) == "" {
		return 0, nil, ErrSSEDisabled
	}
	if strings.TrimSpace(token) == "" {
		return 0, nil, ErrSSETokenMissing
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(s.sseToken)) != 1 {
		return 0, nil, ErrSSETokenInvalid
	}

	id := s.sseSeq.Add(1)
	ch := make(chan []byte, 16)

	s.sseMu.Lock()
	s.sseClients[id] = ch
	s.sseMu.Unlock()

	return id, ch, nil
}

func (s *Service) UnsubscribeSSE(id uint64) {
	s.sseMu.Lock()
	ch, ok := s.sseClients[id]
	if ok {
		delete(s.sseClients, id)
	}
	s.sseMu.Unlock()

	if ok {
		close(ch)
	}
}

func (s *Service) publishSSEPayload(payload []byte) {
	s.sseMu.RLock()
	defer s.sseMu.RUnlock()

	for id, ch := range s.sseClients {
		select {
		case ch <- payload:
		default:
			logger.Debugf("SSE 客户端 %d 发送缓冲区已满，丢弃本次通知", id)
		}
	}
}
