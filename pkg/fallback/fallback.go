package fallback

import (
	"context"
	"sync"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/go-resty/resty/v2"
)

var log = logger.Named("fallback")

const (
	degradeThreshold      = 3
	probeInterval         = 5 * time.Minute
	probeSuccessThreshold = 2
)

// Decision is the next step after interpreting a resty response.
type Decision int

const (
	DecisionOK Decision = iota
	DecisionFallback
	DecisionAbort
)

// InterpretFunc decides how to handle a resty execution result.
type InterpretFunc func(ctx context.Context, resp *resty.Response, err error) Decision

// RequestBuilder configures and executes a request on the given resty request.
type RequestBuilder func(req *resty.Request) (*resty.Response, error)

// Client runs requests on a primary client and optionally retries on a fallback client.
type Client struct {
	primary   *resty.Client
	fallback  *resty.Client
	interpret InterpretFunc

	mu sync.Mutex

	preferFallback     bool
	fallbackFailStreak int
	lastProbeAt        time.Time
	probeSuccessStreak int
	degradedLogged     bool
	recoveredLogged    bool
}

// New creates a fallback client. fallback may be nil to disable cookie retry.
func New(primary, fallback *resty.Client, interpret InterpretFunc) *Client {
	if interpret == nil {
		panic("fallback: interpret func is required")
	}
	return &Client{
		primary:   primary,
		fallback:  fallback,
		interpret: interpret,
	}
}

// Do executes build on the appropriate resty client.
func (c *Client) Do(ctx context.Context, build RequestBuilder) (*resty.Response, error) {
	c.mu.Lock()
	preferFallback := c.preferFallback
	c.mu.Unlock()

	if preferFallback && c.fallback != nil {
		if c.shouldProbeLocked() {
			resp, err := c.execute(ctx, c.primary, build)
			decision := c.interpret(ctx, resp, err)
			if decision == DecisionOK {
				c.onProbeSuccess()
				return resp, nil
			}
			c.onProbeFailure()
		}
		return c.executePrefer(ctx, c.fallback, build)
	}

	resp, err := c.execute(ctx, c.primary, build)
	decision := c.interpret(ctx, resp, err)
	switch decision {
	case DecisionOK:
		return resp, nil
	case DecisionAbort:
		if err != nil {
			return nil, err
		}
		return resp, nil
	case DecisionFallback:
		if c.fallback == nil {
			if err != nil {
				return nil, err
			}
			return resp, nil
		}
		log.Debug("primary 失败，尝试 fallback client")
		c.recordFallbackTrigger()
		fbResp, fbErr := c.execute(ctx, c.fallback, build)
		fbDecision := c.interpret(ctx, fbResp, fbErr)
		if fbDecision == DecisionOK {
			c.onFallbackSuccess()
			return fbResp, nil
		}
		if fbErr != nil {
			return nil, fbErr
		}
		return fbResp, nil
	default:
		if err != nil {
			return nil, err
		}
		return resp, nil
	}
}

func (c *Client) executePrefer(ctx context.Context, client *resty.Client, build RequestBuilder) (*resty.Response, error) {
	resp, err := c.execute(ctx, client, build)
	decision := c.interpret(ctx, resp, err)
	if decision == DecisionOK {
		return resp, nil
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) execute(ctx context.Context, client *resty.Client, build RequestBuilder) (*resty.Response, error) {
	req := client.R().SetContext(ctx)
	return build(req)
}

func (c *Client) shouldProbeLocked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.preferFallback {
		return false
	}
	if c.lastProbeAt.IsZero() || time.Since(c.lastProbeAt) >= probeInterval {
		c.lastProbeAt = time.Now()
		return true
	}
	return false
}

func (c *Client) onProbeSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probeSuccessStreak++
	if c.probeSuccessStreak >= probeSuccessThreshold {
		c.preferFallback = false
		c.fallbackFailStreak = 0
		c.probeSuccessStreak = 0
		c.degradedLogged = false
		if !c.recoveredLogged {
			log.Info("直播間資訊輪詢已恢復匿名模式")
			c.recoveredLogged = true
		}
	}
}

func (c *Client) onProbeFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probeSuccessStreak = 0
}

func (c *Client) recordFallbackTrigger() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fallbackFailStreak++
	if c.fallbackFailStreak == 1 {
		log.Warn("直播間資訊請求觸發風控，已切換為帳號憑證模式")
	}
	if c.fallbackFailStreak >= degradeThreshold && !c.preferFallback {
		c.preferFallback = true
		c.probeSuccessStreak = 0
		c.recoveredLogged = false
		if !c.degradedLogged {
			log.Info("直播間資訊輪詢已降級為帳號憑證模式")
			c.degradedLogged = true
		}
	}
}

func (c *Client) onFallbackSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.preferFallback && c.fallbackFailStreak > 0 {
		// Immediate fallback succeeded without locking preference yet.
		return
	}
}
