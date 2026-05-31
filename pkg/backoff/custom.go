package backoff

import "time"

type Custom struct {
	base time.Duration
	next func(time.Duration) time.Duration

	currnet time.Duration
}

func NewCustom(base time.Duration, next func(time.Duration) time.Duration) *Custom {
	return &Custom{
		base:    base,
		next:    next,
		currnet: base,
	}
}

func (c *Custom) SleepNext() {
	time.Sleep(c.Next())
}

func (c *Custom) Next() time.Duration {
	delay := c.currnet
	c.currnet = c.next(c.currnet)
	return delay
}

func (c *Custom) Reset() {
	c.currnet = c.base
}
