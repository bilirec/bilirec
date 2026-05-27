package bilibili

import (
	"sort"
	"strings"

	"github.com/bilirec/bilirec/pkg/ds"
	"github.com/bilirec/bilirec/pkg/fp"
)

type Codec string

const (
	CodecAVC   Codec = "0"
	CodecHEVC  Codec = "1"
	CodecOther Codec = "2"
)

type StreamProfile string

const (
	ProfileHTTPFLV StreamProfile = "http-flv"
	ProfileHLSTS   StreamProfile = "hls-ts"
	ProfileHLSFMP4 StreamProfile = "hls-fmp4"
)

type GetStreamURLsOption func(*getStreamURLsOptions)

type getStreamURLsOptions struct {
	profiles []StreamProfile
	codecs   []Codec
}

func defaultGetStreamURLsOptions() getStreamURLsOptions {
	return getStreamURLsOptions{
		profiles: []StreamProfile{ProfileHTTPFLV, ProfileHLSTS, ProfileHLSFMP4},
		codecs:   []Codec{CodecAVC, CodecHEVC, CodecOther},
	}
}

func WithProfiles(profiles ...StreamProfile) GetStreamURLsOption {
	return func(o *getStreamURLsOptions) {
		o.profiles = profiles
	}
}

func WithCodecs(codecs ...Codec) GetStreamURLsOption {
	return func(o *getStreamURLsOptions) {
		o.codecs = codecs
	}
}

func profileQueryParams(profiles []StreamProfile) (protocol string, format string) {
	protocolSet := ds.NewSet[string]()
	formatSet := ds.NewSet[string]()

	for _, profile := range profiles {
		switch profile {
		case ProfileHTTPFLV:
			protocolSet.Add("0")
			formatSet.Add("0")
		case ProfileHLSTS:
			protocolSet.Add("1")
			formatSet.Add("1")
		case ProfileHLSFMP4:
			protocolSet.Add("1")
			formatSet.Add("2")
		}
	}

	protocols := protocolSet.ToSlice()
	formats := formatSet.ToSlice()
	sort.Strings(protocols)
	sort.Strings(formats)

	return strings.Join(protocols, ","), strings.Join(formats, ",")
}

func joinCodecs(codecs []Codec) string {
	return strings.Join(fp.Map(codecs, func(c Codec) string { return string(c) }), ",")
}
