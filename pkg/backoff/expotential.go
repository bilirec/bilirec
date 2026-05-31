package backoff

import "time"

type Expotential struct {
	base     time.Duration
	factor   float64
	maxDelay time.Duration

	current time.Duration
}

func NewExpotential(base time.Duration, factor float64, maxDelay time.Duration) *Expotential {
	return &Expotential{
		base:     base,
		factor:   factor,
		maxDelay: maxDelay,
		current:  base,
	}
}

func (e *Expotential) SleepNext() {
	time.Sleep(e.Next())
}

func (e *Expotential) Next() time.Duration {
	delay := e.current
	next := time.Duration(float64(e.current) * e.factor)
	if next > e.maxDelay {
		e.current = e.maxDelay
	} else {
		e.current = next
	}
	return delay
}

func (e *Expotential) Reset() {
	e.current = e.base
}