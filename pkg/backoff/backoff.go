package backoff

import "time"

type Backoff interface {
	SleepNext()
	Next() time.Duration
	Reset()
}
