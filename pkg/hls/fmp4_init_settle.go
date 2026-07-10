package hls

import (
	"bytes"
	"time"

	"github.com/sirupsen/logrus"
)

// DefaultFmp4InitDebounceWindow is the hold-and-settle window after a true
// init-byte change on fMP4 (EXT-X-MAP) playlists. Hardcoded product default.
const DefaultFmp4InitDebounceWindow = 2 * time.Second

// Fmp4InitDebounceWindow is the active debounce window. Tests may shorten it.
var Fmp4InitDebounceWindow = DefaultFmp4InitDebounceWindow

// BytesReleaser returns owned buffers to a pool (optional).
type BytesReleaser func([]byte)

// InitSettle holds fMP4 init changes until the debounce window settles.
// TS / empty-MapURI playlists never use this.
//
// Integrity rules:
//   - URI-only (same init bytes): no rotate, do not re-deliver init
//   - True init change: hold media, do not write into the previous file
//   - Churn back to confirmed: cancel and drop buffered media
type InitSettle struct {
	Window  time.Duration
	Release BytesReleaser
	Log     *logrus.Entry

	confirmed []byte
	active    bool
	pending   []byte
	buf       [][]byte
}

// Active reports whether settle is holding a pending init.
func (s *InitSettle) Active() bool { return s.active }

// BufferMedia appends a media fragment while settle is active.
func (s *InitSettle) BufferMedia(data []byte) {
	if !s.active {
		return
	}
	s.buf = append(s.buf, data)
}

// Reset clears settle state (e.g. m3u8 URL refresh). Drops buffered media.
func (s *InitSettle) Reset(reason string) {
	s.cancel(reason)
	s.confirmed = nil
}

// Cancel drops pending init and buffered media, leaving confirmed unchanged.
func (s *InitSettle) Cancel(reason string) {
	s.cancel(reason)
}

func (s *InitSettle) cancel(reason string) {
	if !s.active {
		return
	}
	s.dropBuf(reason)
	s.active = false
	s.release(s.pending)
	s.pending = nil
}

func (s *InitSettle) dropBuf(reason string) {
	if len(s.buf) == 0 {
		return
	}
	var total int
	for _, frag := range s.buf {
		total += len(frag)
		s.release(frag)
	}
	s.Log.Warnf("hls：fMP4 init 防抖取消（%s），丢弃缓冲媒体 %d 片 / %d B", reason, len(s.buf), total)
	s.buf = nil
}

func (s *InitSettle) release(b []byte) {
	if s.Release != nil {
		s.Release(b)
	}
}

func (s *InitSettle) window() time.Duration {
	if s.Window > 0 {
		return s.Window
	}
	return Fmp4InitDebounceWindow
}

// AcceptMapResult is the outcome of AcceptMap.
type AcceptMapResult struct {
	// DeliverNow is set when the init should be sent on the stream immediately
	// (caller takes ownership). Nil when the map bytes were released/held.
	DeliverNow []byte
	// ArmTimer means the debounce timer should be (re)armed for Window.
	ArmTimer bool
	// StopTimer means an active debounce timer should be stopped.
	StopTimer bool
}

// AcceptMap decides whether to deliver, hold, or skip an init segment.
// It takes ownership of mapData: returns it in DeliverNow, holds it as
// pending (until Settle transfers ownership or Cancel/Reset releases it),
// or releases it. confirmed always keeps a private copy for Equal checks.
func (s *InitSettle) AcceptMap(mapData []byte) AcceptMapResult {
	if s.active {
		switch {
		case len(s.confirmed) > 0 && bytes.Equal(mapData, s.confirmed):
			s.cancel("init 回到已确认内容")
			s.release(mapData)
			return AcceptMapResult{StopTimer: true}
		case bytes.Equal(mapData, s.pending):
			s.release(mapData)
			s.Log.Debugf("hls：fMP4 init 防抖期间再次收到相同 pending init，已重置 %v 窗口", s.window())
			return AcceptMapResult{ArmTimer: true}
		default:
			s.dropBuf("pending init 被更新")
			s.release(s.pending)
			s.pending = mapData
			s.Log.Infof("hls：fMP4 init 防抖期间 init 内容再次变更，已更新 pending 并重置 %v 窗口", s.window())
			return AcceptMapResult{ArmTimer: true}
		}
	}

	if len(s.confirmed) > 0 && bytes.Equal(mapData, s.confirmed) {
		s.release(mapData)
		s.Log.Infof("hls：EXT-X-MAP URI 变更但 init 内容未变（%d B），继续当前文件", len(s.confirmed))
		return AcceptMapResult{}
	}

	if len(s.confirmed) == 0 {
		s.confirmed = append(s.confirmed[:0], mapData...)
		return AcceptMapResult{DeliverNow: mapData}
	}

	s.pending = mapData
	s.active = true
	s.buf = nil
	s.Log.Infof("hls：检测到 fMP4 init 内容变更（%d B），进入 %v 防抖；媒体将缓冲至窗口结束", len(s.pending), s.window())
	return AcceptMapResult{ArmTimer: true}
}

// Settle ends an active hold and returns pending init + buffered media.
// Caller owns the returned slices (pending is transferred, not copied).
// Safe to call when inactive (returns nils).
func (s *InitSettle) Settle() (init []byte, media [][]byte) {
	if !s.active {
		return nil, nil
	}
	init = s.pending
	media = s.buf
	s.active = false
	s.pending = nil
	s.buf = nil
	s.confirmed = append(s.confirmed[:0], init...)
	s.Log.Infof("hls：fMP4 init 防抖结束，投递新 init（%d B）并刷出缓冲媒体 %d 片", len(init), len(media))
	return init, media
}
