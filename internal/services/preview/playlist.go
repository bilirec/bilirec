package preview

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bilirec/bilirec/pkg/mp4"
)

func (s *Service) Playlist(roomID int) (string, error) {
	v, ok := s.roomView(roomID)
	if !ok || v.path == "" {
		return "", ErrNotRecording
	}
	if v.ext != ".ts" && v.ext != ".fmp4" {
		return "", ErrUnsupportedExt
	}
	st, err := os.Stat(v.path)
	if err != nil {
		return "", ErrSegmentMissing
	}
	fileSize := st.Size()

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:EVENT\n")
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")

	if v.ext == ".fmp4" {
		if !v.hasInit {
			if size, findErr := mp4.InitSize(v.path); findErr == nil && size > 0 {
				v.initOffset = 0
				v.initSize = size
				v.hasInit = true
			}
		}
		if v.hasInit && v.initOffset+v.initSize <= fileSize {
			fmt.Fprintf(&b, "#EXT-X-MAP:URI=\"media?offset=%d&length=%d\"\n", v.initOffset, v.initSize)
		}
	}

	maxDur := 2
	var body strings.Builder
	for _, seg := range v.segs {
		if seg.offset < v.previewStart {
			continue
		}
		end := seg.offset + int64(seg.size)
		if end > fileSize {
			continue
		}
		d := int(seg.durSec + 0.999)
		if d < 1 {
			d = 1
		}
		if d > maxDur {
			maxDur = d
		}
		fmt.Fprintf(&body, "#EXTINF:%s,\nmedia?offset=%d&length=%d\n", strconv.FormatFloat(seg.durSec, 'f', 3, 64), seg.offset, seg.size)
	}
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", maxDur)
	b.WriteString(body.String())
	return b.String(), nil
}
