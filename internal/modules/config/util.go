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

// ReadStreamMaxTagDataSizeForQn returns the largest allowed FLV tag body for the
// given quality tier (read-buffer size * 4).
func ReadStreamMaxTagDataSizeForQn(qn int) int {
	return ReadStreamBytesPoolSizeForQn(qn) * 4
}
