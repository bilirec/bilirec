package coordinator

import (
	"sync"
	"time"
)

// DefaultMinTick is the minimum interval between signals when many participants are active.
const DefaultMinTick = 2 * time.Second

// participant is one registered slot in the round-robin.
type participant struct {
	id     uint64
	signal chan struct{} // cap 1
	ready  func() bool   // nil = always ready
}

// RoundRobin sends a non-blocking signal to one ready participant per tick.
// With N participants, tick interval = CyclePeriod/N so each gets a turn about once per CyclePeriod.
type RoundRobin struct {
	mu           sync.Mutex
	cyclePeriod  time.Duration
	minTick      time.Duration
	participants []*participant
	nextID       uint64
	rr           int
	stop         chan struct{}
	running      bool
}

// NewRoundRobin creates a scheduler. cyclePeriod is the target duration for a full round (all participants).
func NewRoundRobin(cyclePeriod time.Duration) *RoundRobin {
	if cyclePeriod <= 0 {
		cyclePeriod = 10 * time.Second
	}
	return &RoundRobin{
		cyclePeriod: cyclePeriod,
		minTick:     DefaultMinTick,
	}
}

// SetCyclePeriod updates the full-round duration (e.g. when config changes at runtime).
func (r *RoundRobin) SetCyclePeriod(d time.Duration) {
	if d <= 0 {
		return
	}
	r.mu.Lock()
	r.cyclePeriod = d
	r.mu.Unlock()
}

// SetMinTick overrides the minimum tick interval (tests or high participant counts).
func (r *RoundRobin) SetMinTick(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d > 0 {
		r.minTick = d
	}
}

// Register adds a participant.
// ready returns whether this participant should receive a signal when its turn is considered.
// Pass nil to always treat as ready.
// Returns a read-only signal channel and unregister; call unregister on shutdown.
func (r *RoundRobin) Register(ready func() bool) (<-chan struct{}, func()) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	p := &participant{
		id:     r.nextID,
		signal: make(chan struct{}, 1),
		ready:  ready,
	}
	r.participants = append(r.participants, p)
	r.ensureLoopLocked()

	id := p.id
	return p.signal, func() { r.unregister(id) }
}

func (r *RoundRobin) unregister(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.participants {
		if p.id == id {
			r.participants[i] = r.participants[len(r.participants)-1]
			r.participants = r.participants[:len(r.participants)-1]
			break
		}
	}
	if n := len(r.participants); n > 0 && r.rr >= n {
		r.rr %= n
	}
	if len(r.participants) == 0 && r.running {
		close(r.stop)
		r.running = false
	}
}

func (r *RoundRobin) ensureLoopLocked() {
	if r.running {
		return
	}
	r.stop = make(chan struct{})
	r.running = true
	go r.loop()
}

func (r *RoundRobin) tickIntervalLocked() time.Duration {
	n := len(r.participants)
	if n <= 1 {
		return r.cyclePeriod
	}
	d := r.cyclePeriod / time.Duration(n)
	if d < r.minTick {
		return r.minTick
	}
	return d
}

func (r *RoundRobin) isReady(p *participant) bool {
	return p.ready == nil || p.ready()
}

func (r *RoundRobin) loop() {
	ticker := time.NewTicker(r.tickIntervalLocked())
	defer ticker.Stop()

	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			var target *participant

			r.mu.Lock()
			n := len(r.participants)
			if n == 0 {
				r.mu.Unlock()
				continue
			}
			if r.rr >= n {
				r.rr %= n
			}
			ticker.Reset(r.tickIntervalLocked())

			start := r.rr
			for i := 0; i < n; i++ {
				candidate := r.participants[r.rr]
				r.rr = (r.rr + 1) % n
				if r.isReady(candidate) {
					target = candidate
					break
				}
				if r.rr == start {
					break
				}
			}
			r.mu.Unlock()

			if target != nil {
				select {
				case target.signal <- struct{}{}:
				default:
				}
			}
		}
	}
}
