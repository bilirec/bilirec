package config

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
