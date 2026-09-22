package config

import "strings"

// ParseWebhookURLs splits WEBHOOK_URLS (newline-separated) into POST targets.
func ParseWebhookURLs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	for line := range strings.SplitSeq(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// WebhookConfigured reports whether WEBHOOK_URLS contains at least one non-empty URL line.
func (c *Config) WebhookConfigured() bool {
	if c == nil {
		return false
	}
	return len(ParseWebhookURLs(c.WebhookURLs)) > 0
}

// IsHighQualityQn reports whether the stream quality level uses the high-tier
// read-buffer pool (2K/4K and above).
func IsHighQualityQn(qn int) bool {
	return qn >= 20000
}

// ReadStreamBytesPoolSizeForQn returns the read-buffer size tier used by the stream
// service and FLV realtime fixer for the given quality level.
func ReadStreamBytesPoolSizeForQn(qn int) int {
	if IsHighQualityQn(qn) {
		return ReadOnly.ReadStreamBytesPoolSizeHigh()
	}
	return ReadOnly.ReadStreamBytesPoolSize()
}

func clampInt(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

// ReadStreamBytesPoolBoundedCapacity derives idle read-pool capacity from channel depth.
func ReadStreamBytesPoolBoundedCapacity(chanBufferSize int) int {
	if chanBufferSize <= 0 {
		chanBufferSize = 16
	}
	return clampInt(chanBufferSize/4, 2, 16)
}

// LiveStreamWriterBytesPoolBoundedCapacityPerBucket derives per-bucket idle writer-pool capacity.
func LiveStreamWriterBytesPoolBoundedCapacityPerBucket(chanBufferSize int) int {
	if chanBufferSize <= 0 {
		chanBufferSize = 64
	}
	return clampInt(chanBufferSize/8, 2, 8)
}
