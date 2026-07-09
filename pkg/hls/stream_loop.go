package hls

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"
)

// StreamRunnerOptions configures NewStreamRunner / Start.
type StreamRunnerOptions struct {
	Session        *PlaylistSession
	Settle         *InitSettle
	SegmentClient  *resty.Client
	ReadBody       SegmentBodyReader
	ReleaseBytes   BytesReleaser
	Log            *logrus.Entry
	ChanBufferSize int
	OnClose        func()

	// Initial playlist state after the first successful fetch.
	InitialPlaylist *Playlist
	PollInterval    time.Duration
	NextSeq         int64
	PrevBaseSeq     int64
}

// StreamRunner owns the poll/delivery loop after playlist session is ready.
type StreamRunner struct {
	ctx           context.Context
	session       *PlaylistSession
	settle        *InitSettle
	segmentClient *resty.Client
	readBody      SegmentBodyReader
	ch            chan<- []byte
	release       BytesReleaser
	log           *logrus.Entry
	onClose       func()
	pollInterval  time.Duration
	nextSeq       int64
	prevBaseSeq   int64
	currentMapURI string
	mapSent       bool
	debounceTimer *time.Timer
}

// Start validates options, creates the output channel, and starts the runner goroutine.
func Start(ctx context.Context, opt StreamRunnerOptions) (<-chan []byte, error) {
	if opt.Session == nil || opt.Settle == nil || opt.SegmentClient == nil || opt.InitialPlaylist == nil {
		return nil, fmt.Errorf("hls：StreamRunner 缺少必要参数")
	}
	if opt.Log == nil {
		return nil, fmt.Errorf("hls：StreamRunner 缺少 Log")
	}
	if opt.Settle.Log == nil {
		return nil, fmt.Errorf("hls：InitSettle 缺少 Log")
	}
	if opt.PollInterval <= 0 {
		return nil, fmt.Errorf("hls：PollInterval 必须大于 0")
	}
	if opt.Session.Prefetcher() == nil || opt.Session.Resolver() == nil {
		return nil, fmt.Errorf("hls：PlaylistSession 尚未 RefreshURL")
	}
	if opt.ReadBody == nil {
		opt.ReadBody = readSegmentBody
	}
	if opt.ReleaseBytes == nil {
		opt.ReleaseBytes = func([]byte) {}
	}
	if opt.ChanBufferSize < 1 {
		opt.ChanBufferSize = 16
	}
	ch := make(chan []byte, opt.ChanBufferSize)
	runner := &StreamRunner{
		ctx:           ctx,
		session:       opt.Session,
		settle:        opt.Settle,
		segmentClient: opt.SegmentClient,
		readBody:      opt.ReadBody,
		ch:            ch,
		release:       opt.ReleaseBytes,
		log:           opt.Log,
		onClose:       opt.OnClose,
		pollInterval:  opt.PollInterval,
		nextSeq:       opt.NextSeq,
		prevBaseSeq:   opt.PrevBaseSeq,
		currentMapURI: opt.InitialPlaylist.MapURI,
		mapSent:       false,
	}
	go runner.run()
	return ch, nil
}

func (r *StreamRunner) stopDebounceTimer() {
	if !r.debounceTimer.Stop() {
		select {
		case <-r.debounceTimer.C:
		default:
		}
	}
}

func (r *StreamRunner) armDebounceTimer() {
	r.stopDebounceTimer()
	r.debounceTimer.Reset(r.settle.window())
}

func (r *StreamRunner) sendSettle() bool {
	initSeg, media := r.settle.Settle()
	if initSeg == nil {
		return true
	}
	r.stopDebounceTimer()
	select {
	case r.ch <- initSeg:
	case <-r.ctx.Done():
		for _, frag := range media {
			r.release(frag)
		}
		return false
	}
	for i, frag := range media {
		select {
		case r.ch <- frag:
		case <-r.ctx.Done():
			for _, remaining := range media[i:] {
				r.release(remaining)
			}
			return false
		}
	}
	return true
}

func (r *StreamRunner) acceptMap(mapData []byte) bool {
	res := r.settle.AcceptMap(mapData)
	r.mapSent = true
	if res.StopTimer {
		r.stopDebounceTimer()
	}
	if res.ArmTimer {
		r.armDebounceTimer()
	}
	if res.DeliverNow != nil {
		select {
		case r.ch <- res.DeliverNow:
			return true
		case <-r.ctx.Done():
			r.release(res.DeliverNow)
			return false
		}
	}
	return true
}

func (r *StreamRunner) run() {
	defer close(r.ch)
	if r.onClose != nil {
		defer r.onClose()
	}

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	syncWaitTimer := time.NewTimer(time.Hour)
	if !syncWaitTimer.Stop() {
		select {
		case <-syncWaitTimer.C:
		default:
		}
	}
	defer syncWaitTimer.Stop()

	r.debounceTimer = time.NewTimer(time.Hour)
	if !r.debounceTimer.Stop() {
		select {
		case <-r.debounceTimer.C:
		default:
		}
	}
	defer r.debounceTimer.Stop()

	defer func() {
		if r.settle.Active() {
			_ = r.sendSettle()
		}
	}()

	consecutivePlaylistFailures := 0
	consecutiveSegmentFailures := 0
	segmentFailureBackoff := backoff.NewExpotential(2*time.Second, 2, 30*time.Second)
	lastSyncWaitBaseSeq := int64(-1)
	traceEnabled := r.log.Logger.IsLevelEnabled(logrus.TraceLevel)

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.debounceTimer.C:
			if !r.sendSettle() {
				return
			}
		case <-ticker.C:
			if !r.onTick(
				ticker,
				syncWaitTimer,
				&consecutivePlaylistFailures,
				&consecutiveSegmentFailures,
				segmentFailureBackoff,
				&lastSyncWaitBaseSeq,
				traceEnabled,
			) {
				return
			}
		}
	}
}

func (r *StreamRunner) onTick(
	ticker *time.Ticker,
	syncWaitTimer *time.Timer,
	consecutivePlaylistFailures *int,
	consecutiveSegmentFailures *int,
	segmentFailureBackoff *backoff.Expotential,
	lastSyncWaitBaseSeq *int64,
	traceEnabled bool,
) bool {
	pl, err := r.session.FetchWithRefresh()
	if err != nil {
		if errors.Is(err, ErrNoM3u8URL) {
			r.log.Infof("hls：已无可用 m3u8 URL，直播可能已结束")
			return false
		}
		if isCanceled(err, r.ctx) || r.ctx.Err() == context.Canceled {
			return false
		}
		*consecutivePlaylistFailures++
		r.log.Warnf("hls：拉取/解析播放列表失败（第 %d 次）：%v", *consecutivePlaylistFailures, err)

		pl, err = r.session.FetchWithRefresh()
		if err != nil {
			if errors.Is(err, ErrNoM3u8URL) {
				r.log.Infof("hls：已无可用 m3u8 URL，直播可能已结束")
				return false
			}
			if isCanceled(err, r.ctx) || r.ctx.Err() == context.Canceled {
				return false
			}
			*consecutivePlaylistFailures++
			r.log.Warnf("hls：立即重试播放列表失败（第 %d 次）：%v", *consecutivePlaylistFailures, err)
			if *consecutivePlaylistFailures >= 3 {
				r.log.Warnf("hls：播放列表连续失败次数达到 %d", *consecutivePlaylistFailures)
				return false
			}
			return true
		}
		r.log.Warnf("hls：通过立即重试恢复了播放列表")
	}

	if *consecutivePlaylistFailures > 0 {
		r.log.Infof("hls：在 %d 次失败后拉取/解析播放列表已恢复", *consecutivePlaylistFailures)
	}
	*consecutivePlaylistFailures = 0

	if *consecutiveSegmentFailures > 0 {
		waitFor := segmentFailureBackoff.Next()
		r.log.Debugf("hls: segment failure backoff wait=%v failures=%d", waitFor, *consecutiveSegmentFailures)
		backoffTimer := time.NewTimer(waitFor)
		select {
		case <-backoffTimer.C:
		case <-r.ctx.Done():
			backoffTimer.Stop()
			return false
		}

		if *consecutiveSegmentFailures%SegmentFailureRefreshEvery == 0 {
			r.log.Warnf("hls：连续 %d 轮分片拉取失败，正在刷新 m3u8 URL", *consecutiveSegmentFailures)
			if refreshErr := r.session.RefreshURL("segment failures"); refreshErr != nil {
				r.log.Warnf("hls：刷新 m3u8 URL 失败：%v", refreshErr)
			} else {
				r.currentMapURI = ""
				r.mapSent = false
				refreshedPl, refreshFetchErr := r.session.FetchWithRefresh()
				if refreshFetchErr != nil {
					if errors.Is(refreshFetchErr, ErrNoM3u8URL) {
						r.log.Infof("hls：已无可用 m3u8 URL，直播可能已结束")
						return false
					}
					if isCanceled(refreshFetchErr, r.ctx) || r.ctx.Err() == context.Canceled {
						return false
					}
					r.log.Warnf("hls：刷新 m3u8 后拉取播放列表失败：%v", refreshFetchErr)
				} else {
					pl = refreshedPl
				}
			}
		}
	}

	updatedPollInterval := DerivePollInterval(pl)
	if updatedPollInterval != r.pollInterval {
		r.log.Infof("hls：轮询间隔已从 %v 更新为 %v（target=%.2fs，first-extinf=%.2fs）", r.pollInterval, updatedPollInterval, pl.TargetDuration, func() float64 {
			if len(pl.Segments) > 0 {
				return pl.Segments[0].Duration
			}
			return 0
		}())
		r.pollInterval = updatedPollInterval
		ticker.Reset(r.pollInterval)
	}

	baseSeq, segs := pl.MediaSeq, pl.Segments
	if traceEnabled {
		pendingSegments := CountPendingSegments(baseSeq, segs, r.nextSeq)
		r.log.Tracef("hls: playlist window base=%d len=%d next=%d pending=%d map=%t", baseSeq, len(segs), r.nextSeq, pendingSegments, pl.MapURI != "")
	}

	if baseSeq > r.nextSeq {
		lost := baseSeq - r.nextSeq
		r.log.Warnf("hls：检测到序列间隙，可能丢失了 %d 个分片（nextSeq=%d，baseSeq=%d）", lost, r.nextSeq, baseSeq)
		r.nextSeq = baseSeq
	}
	if ShouldResetSequenceOnRollback(r.prevBaseSeq, baseSeq, len(segs), r.nextSeq) {
		r.log.Warnf("hls：检测到序列回退/不连续（nextSeq=%d，baseSeq=%d，window=%d），正在重置 nextSeq", r.nextSeq, baseSeq, len(segs))
		r.nextSeq = baseSeq
		r.mapSent = false
	}

	if pl.MapURI != r.currentMapURI {
		if pl.MapURI == "" && r.settle.Active() {
			r.settle.Cancel("playlist 已无 EXT-X-MAP")
			r.stopDebounceTimer()
		}
		if r.currentMapURI != "" && pl.MapURI != "" && r.currentMapURI != pl.MapURI {
			r.log.Infof("hls：EXT-X-MAP 已变更（%s → %s），将在拉取新 init 后判定是否防抖/切文件", r.currentMapURI, pl.MapURI)
		}
		r.currentMapURI = pl.MapURI
		r.mapSent = false
	}

	if len(segs) > 0 && ShouldApplyManifestSyncWait(baseSeq, r.nextSeq, *lastSyncWaitBaseSeq) {
		waitForSync := DeriveManifestSyncWait(pl, ManifestSyncWaitRate)
		r.log.Debugf("hls: applying manifest sync wait=%v base=%d next=%d", waitForSync, baseSeq, r.nextSeq)
		syncWaitTimer.Reset(waitForSync)
		select {
		case <-syncWaitTimer.C:
			*lastSyncWaitBaseSeq = baseSeq
			r.log.Debugf("hls: manifest sync wait completed base=%d", baseSeq)
		case <-r.ctx.Done():
			if !syncWaitTimer.Stop() {
				select {
				case <-syncWaitTimer.C:
				default:
				}
			}
			return false
		}
	}

	if !r.deliverWindow(baseSeq, segs, consecutiveSegmentFailures, segmentFailureBackoff, traceEnabled) {
		return false
	}
	r.prevBaseSeq = baseSeq
	return true
}

func (r *StreamRunner) deliverWindow(
	baseSeq int64,
	segs []Segment,
	consecutiveSegmentFailures *int,
	segmentFailureBackoff *backoff.Expotential,
	traceEnabled bool,
) bool {
	maxSeqInWindow := baseSeq + int64(len(segs)) - 1
	prefetchAhead := SegmentPrefetchAhead
	if *consecutiveSegmentFailures > 0 {
		prefetchAhead = 0
	}
	prefetcher := r.session.Prefetcher()
	resolver := r.session.Resolver()
	if r.nextSeq <= maxSeqInWindow {
		primeEndSeq := r.nextSeq + int64(prefetchAhead)
		if primeEndSeq > maxSeqInWindow {
			primeEndSeq = maxSeqInWindow
		}
		for seq := r.nextSeq; seq <= primeEndSeq; seq++ {
			idx := int(seq - baseSeq)
			prefetcher.Start(seq, segs[idx].URI)
		}
	}
	nextPrefetchSeq := r.nextSeq + int64(prefetchAhead) + 1

	segmentDelivered := false
	for i, seg := range segs {
		if r.settle.Active() {
			select {
			case <-r.debounceTimer.C:
				if !r.sendSettle() {
					return false
				}
			default:
			}
		}

		segSeq := baseSeq + int64(i)
		if segSeq < r.nextSeq {
			continue
		}

		if r.currentMapURI != "" && !r.mapSent {
			mapFetchStart := time.Now()
			mapURL, err := resolver.Resolve(r.currentMapURI)
			if err != nil {
				r.log.Warnf("hls：无法解析 map URL %q：%v", r.currentMapURI, err)
				break
			}

			mapResp, err := r.segmentClient.R().SetContext(r.ctx).SetDoNotParseResponse(true).Get(mapURL)
			if err != nil {
				if isCanceled(err, r.ctx) {
					return false
				}
				r.log.Warnf("hls：拉取 map 失败：%v", err)
				break
			}
			if mapResp.StatusCode() != 200 {
				r.log.Warnf("hls：map 状态码 %d", mapResp.StatusCode())
				break
			}
			mapData, readErr := r.readBody(mapResp)
			if readErr != nil {
				if isCanceled(readErr, r.ctx) {
					return false
				}
				r.log.Warnf("hls：读取 map 失败：%v", readErr)
				break
			}
			r.log.Debugf("hls: map fetch ok bytes=%d elapsed=%v", len(mapData), time.Since(mapFetchStart))

			if !r.acceptMap(mapData) {
				return false
			}
		}

		if nextPrefetchSeq <= maxSeqInWindow {
			prefetchIdx := int(nextPrefetchSeq - baseSeq)
			if traceEnabled {
				r.log.Tracef("hls: prefetch start seq=%d uri=%s", nextPrefetchSeq, segs[prefetchIdx].URI)
			}
			prefetcher.Start(nextPrefetchSeq, segs[prefetchIdx].URI)
			nextPrefetchSeq++
		}

		waitStart := time.Now()
		data, err := prefetcher.Wait(segSeq, seg.URI)
		if err != nil {
			if isCanceled(err, r.ctx) || r.ctx.Err() == context.Canceled {
				return false
			}
			*consecutiveSegmentFailures++
			r.log.Warnf("hls：拉取分片失败（seq=%d，连续第 %d 轮）：%v", segSeq, *consecutiveSegmentFailures, err)
			break
		}
		if traceEnabled {
			r.log.Tracef("hls: segment ready seq=%d bytes=%d wait=%v", segSeq, len(data), time.Since(waitStart))
		}

		r.nextSeq = segSeq + 1
		segmentDelivered = true

		if r.settle.Active() {
			r.settle.BufferMedia(data)
			continue
		}

		select {
		case r.ch <- data:
		case <-r.ctx.Done():
			r.release(data)
			return false
		}
	}

	if segmentDelivered {
		*consecutiveSegmentFailures = 0
		segmentFailureBackoff.Reset()
	}
	return true
}
