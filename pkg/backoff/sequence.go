package backoff

import "time"

type Sequence struct {
	delays []time.Duration
	index  int
}

func NewSequence(delays ...time.Duration) *Sequence {
	return &Sequence{
		delays: delays,
		index:  0,
	}
}

func (s *Sequence) SleepNext() {
	time.Sleep(s.Next())
}

func (s *Sequence) Next() time.Duration {
	if s.index >= len(s.delays) {
		return s.delays[len(s.delays)-1]
	}
	delay := s.delays[s.index]
	s.index++
	return delay
}

func (s *Sequence) Reset() {
	s.index = 0
}
