package bilibili

import (
	"fmt"
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

func (c Codec) String() string {
	switch c {
	case CodecAVC:
		return "AVC"
	case CodecHEVC:
		return "HEVC"
	case CodecOther:
		return "其他"
	default:
		return fmt.Sprintf("未知(%s)", string(c))
	}
}

func (c Codec) IsValid() bool {
	switch c {
	case CodecAVC, CodecHEVC, CodecOther:
		return true
	default:
		return false
	}
}

type StreamProfile string

const (
	ProfileHTTPFLV StreamProfile = "http-flv"
	ProfileHLSTS   StreamProfile = "hls-ts"
	ProfileHLSFMP4 StreamProfile = "hls-fmp4"
)

func (p StreamProfile) String() string {
	switch p {
	case ProfileHTTPFLV:
		return "http-flv"
	case ProfileHLSTS:
		return "hls-ts"
	case ProfileHLSFMP4:
		return "hls-fmp4"
	default:
		return fmt.Sprintf("未知(%s)", string(p))
	}
}

func (p StreamProfile) IsValid() bool {
	switch p {
	case ProfileHTTPFLV, ProfileHLSTS, ProfileHLSFMP4:
		return true
	default:
		return false
	}
}

type Quality int

const (
	QualitySmooth   Quality = 80
	QualityHigh     Quality = 150
	QualitySuper    Quality = 250
	QualityBluRay   Quality = 400
	QualityOriginal Quality = 10000
	Quality4K       Quality = 20000
	QualityDolby    Quality = 30000
)

func (q Quality) String() string {
	switch q {
	case QualitySmooth:
		return "流畅"
	case QualityHigh:
		return "高清"
	case QualitySuper:
		return "超清"
	case QualityBluRay:
		return "蓝光"
	case QualityOriginal:
		return "原画"
	case Quality4K:
		return "4K"
	case QualityDolby:
		return "杜比"
	default:
		return fmt.Sprintf("未知(%d)", int(q))
	}
}

func (q Quality) IsValid() bool {
	switch q {
	case QualitySmooth, QualityHigh, QualitySuper, QualityBluRay, QualityOriginal, Quality4K, QualityDolby:
		return true
	default:
		return false
	}
}

type GetStreamURLsOption func(*getStreamURLsOptions)

type getStreamURLsOptions struct {
	profiles  []StreamProfile
	codecs    []Codec
	qn        Quality
	onlyAudio bool
}

func defaultGetStreamURLsOptions() getStreamURLsOptions {
	return getStreamURLsOptions{
		profiles:  []StreamProfile{ProfileHTTPFLV, ProfileHLSTS, ProfileHLSFMP4},
		codecs:    []Codec{CodecAVC, CodecHEVC, CodecOther},
		qn:        QualityOriginal, // matches READ_STREAM defaults; auto-fallback still applies if unavailable
		onlyAudio: false,
	}
}

func WithQn(qn Quality) GetStreamURLsOption {
	return func(o *getStreamURLsOptions) {
		o.qn = qn
	}
}

func WithOnlyAudio(onlyAudio bool) GetStreamURLsOption {
	return func(o *getStreamURLsOptions) {
		o.onlyAudio = onlyAudio
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
