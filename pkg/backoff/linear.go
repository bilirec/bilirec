package backoff

import "time"

type Linear struct {
	base     time.Duration
	step     time.Duration
	maxDelay time.Duration

	current time.Duration
}

func NewLinear(base, step, maxDelay time.Duration) *Linear {
	return &Linear{
		base:     base,
		step:     step,
		maxDelay: maxDelay,
		current:  base,
	}
}

func (l *Linear) SleepNext() {
	time.Sleep(l.Next())
}

func (l *Linear) Next() time.Duration {
	delay := l.current
	if l.current < l.maxDelay {
		l.current += l.step
		if l.current > l.maxDelay {
			l.current = l.maxDelay
		}
	}
	return delay
}

func (l *Linear) Reset() {
	l.current = l.base
}
