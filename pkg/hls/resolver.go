package hls

import (
	"net/url"
	"strings"
)

// URLResolver resolves relative segment URIs against playlist URL.
type URLResolver struct {
	base *url.URL
}

// NewURLResolver creates a URL resolver from a playlist URL.
func NewURLResolver(m3u8URL string) (*URLResolver, error) {
	base, err := url.Parse(m3u8URL)
	if err != nil {
		return nil, err
	}
	return &URLResolver{base: base}, nil
}

// Resolve resolves segmentURI against base playlist URL.
func (r *URLResolver) Resolve(segmentURI string) (string, error) {
	if strings.HasPrefix(segmentURI, "http://") || strings.HasPrefix(segmentURI, "https://") {
		return segmentURI, nil
	}
	ref, err := url.Parse(segmentURI)
	if err != nil {
		return "", err
	}
	return r.base.ResolveReference(ref).String(), nil
}
