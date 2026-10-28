package recorder

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
	"github.com/bilirec/bilirec/internal/services/stream"
	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/bilirec/bilirec/pkg/tx"
	"github.com/bilirec/bilirec/utils"
)

type startMode int

const (
	startModeUser startMode = iota
	startModeRecovery
)

type internalStartParams struct {
	roomId  int
	opts    RecordStartOptions
	ctx     context.Context
	cancel  context.CancelFunc // user mode only; adopted by Info on success
	mode    startMode
	session *Info // recovery only
}

func (r *Service) internalStart(p internalStartParams) error {
	l := log.With("room", p.roomId)

	r.m.StreamConnectAttempt(p.roomId)

	txn := r.reser.Begin()
	if err := txn.Reserve(p.roomId); err != nil {
		if errors.Is(err, tx.ErrAlreadyReserved) {
			return ErrRecordingPending
		}
		return err
	}
	defer txn.Abort(p.roomId)

	if err := p.ctx.Err(); err != nil {
		return err
	}

	if err := r.ensureDiskSpace(l, p); err != nil {
		return err
	}

	startTimeRoomInfo := time.Now()
	roomInfo, err := r.bilic.GetLiveRoomInfo(p.roomId)
	durationRoomInfo := time.Since(startTimeRoomInfo)
	l.Debugf("duration: function=GetLiveRoomInfo spent=%v", durationRoomInfo)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrLiveAPI, err)
	} else if roomInfo.IsEncrypted {
		return ErrRoomEncrypted
	} else if roomInfo.LockStatus != 0 {
		return ErrRoomBanned
	} else if roomInfo.LiveStatus != 1 {
		return ErrStreamNotLive
	}

	startTimeStreamURLs := time.Now()
	streams, playExtra, err := r.bilic.GetStreamURLsV2WithExtra(p.roomId, p.opts.streamOptions...)
	durationStreamURLs := time.Since(startTimeStreamURLs)
	l.Debugf("duration: function=GetStreamURLsV2 spent=%v", durationStreamURLs)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrLiveAPI, err)
	} else if len(streams) == 0 {
		return ErrEmptyStreamURLs
	}

	now := time.Now()
	ctx := p.ctx

	for idx, streamInfo := range streams {
		if err := ctx.Err(); err != nil {
			return err
		}

		urlPreview := utils.TruncateString(streamInfo.URL, 160)
		l.Debugf("trying stream url [%d/%d]: protocol=%s, format=%s, codec=%s, qn=%d, url=%s",
			idx+1,
			len(streams),
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			urlPreview,
		)

		ch, strategy, chunkPool, connected, err := r.tryConnectStream(l, p.roomId, ctx, streamInfo, urlPreview, idx+1, len(streams))
		if err != nil {
			return err
		}
		if !connected {
			continue
		}

		if !r.sessionReadyForConfirm(p) {
			discardStreamCh(ch, chunkPool)
			if err := ctx.Err(); err != nil {
				return err
			}
			return context.Canceled
		}

		var maxDuration time.Duration
		if p.opts.hasDuration {
			maxDuration = p.opts.duration
		} else {
			maxDuration = time.Duration(r.cfg.MaxRecordingHours) * time.Hour
		}

		info, err := r.commitSession(p, txn, roomInfo, now, maxDuration)
		if err != nil {
			discardStreamCh(ch, chunkPool)
			return err
		}
		if playExtra != nil {
			info.liveSubtitleEnabled.Store(playExtra.SubtitleEnabled)
		}
		info.chunkPool = chunkPool
		info.SetStream(streamInfo.Qn, streamInfo.IsAudioOnly, streamInfo.Format)

		if p.mode == startModeUser {
			r.emitSessionStarted(info)
		}

		return r.prepare(p.roomId, ch, strategy, info.ctx, info, p.mode == startModeUser)
	}

	l.Warn("no more url left")
	return ErrStreamURLsUnreachable
}

func isInsufficientDiskSpace(free uint64, minimum int64) bool {
	if minimum <= 0 {
		return false
	}
	return free < uint64(minimum)
}

func (r *Service) sessionReadyForConfirm(p internalStartParams) bool {
	if err := p.ctx.Err(); err != nil {
		return false
	}
	if p.mode != startModeRecovery {
		return true
	}
	existing, ok := r.recording.Load(p.roomId)
	return ok && existing == p.session
}

func (r *Service) commitSession(
	p internalStartParams,
	txn tx.Txn[int],
	roomInfo *bilibili.LiveRoomInfoDetail,
	now time.Time,
	maxDuration time.Duration,
) (*Info, error) {
	if p.mode == startModeRecovery {
		info := p.session
		info.room = roomInfo
		info.fileTime = nextFileTime(info.fileTime, now)
		if r.cfg.RecordingRecoveryDuration == "reset" {
			info.startTime = now
		}
		// Recovery reuses the session's existing options. Keep them immutable
		// after the session is published so concurrent stats reads stay safe.
		txn.ConfirmWith(p.roomId, func() {
			info.status.Store(recordingPtr)
		})
		if err := info.ctx.Err(); err != nil {
			return nil, err
		}
		if existing, ok := r.recording.Load(p.roomId); !ok || existing != info {
			return nil, context.Canceled
		}
		return info, nil
	}

	sessionID, err := utils.NewUUIDv4()
	if err != nil {
		return nil, fmt.Errorf("无法生成录制会话 ID：%w", err)
	}

	info := &Info{
		ctx:          p.ctx,
		cancel:       p.cancel,
		startOptions: snapshotStartOptions(p.opts),
		startTime:    now,
		fileTime:     now,
		room:         roomInfo,
		maxDuration:  maxDuration,
		sessionID:    sessionID,
		backoff: backoff.NewSequence(
			2*time.Second,
			2*time.Second,
			2*time.Second,
			5*time.Second,
			10*time.Second,
			15*time.Second,
		),
	}
	info.SetOutputPath("")

	txn.ConfirmWith(p.roomId, func() {
		info.status.Store(recordingPtr)
		r.recording.Store(p.roomId, info)
		r.m.RecordingStarted(p.roomId, roomInfo.Uname)
	})

	return info, nil
}

// nextFileTime returns a second-granularity stamp strictly after prev when now
// would otherwise collide with prev's filename second.
func nextFileTime(prev, now time.Time) time.Time {
	stamp := now.Truncate(time.Second)
	prevStamp := prev.Truncate(time.Second)
	if !stamp.After(prevStamp) {
		return prevStamp.Add(time.Second)
	}
	return stamp
}

func (r *Service) tryConnectStream(
	l logger.Logger,
	roomId int,
	ctx context.Context,
	streamInfo bilibili.StreamURLInfo,
	urlPreview string,
	attempt, total int,
) (<-chan []byte, rs.RecordStrategy, *pool.BucketedBytesPool, bool, error) {
	res, err := r.st.TryConnect(ctx, stream.ConnectRequest{
		RoomID:     roomId,
		StreamInfo: streamInfo,
		Log:        l,
		Attempt:    attempt,
		Total:      total,
		URLPreview: urlPreview,
	})
	if err != nil {
		return nil, nil, nil, false, err
	}
	if !res.Connected {
		return nil, nil, nil, false, nil
	}
	strategy, err := rs.NewFor(streamInfo)
	if err != nil {
		discardStreamCh(res.Ch, res.ChunkPool)
		return nil, nil, nil, false, err
	}
	return res.Ch, strategy, res.ChunkPool, true, nil
}

func discardStreamCh(ch <-chan []byte, chunkPool *pool.BucketedBytesPool) {
	if ch == nil {
		return
	}
	go func() {
		for data := range ch {
			if chunkPool != nil && data != nil && cap(data) > 0 {
				chunkPool.Put(data[:cap(data)])
			}
		}
	}()
}
