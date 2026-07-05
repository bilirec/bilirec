package recorder

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/tx"
	"github.com/bilirec/bilirec/utils"
	"github.com/sirupsen/logrus"
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
	l := logger.WithField("room", p.roomId)

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

	diskSpace, err := utils.GetDiskSpace(r.cfg.OutputDir)
	if err != nil {
		l.Warnf("cannot check disk space: %v", err)
	} else if diskSpace.Free < uint64(r.cfg.MinDiskSpaceBytes) {
		return ErrInsufficientDiskSpace
	}

	startTimeRoomInfo := time.Now()
	roomInfo, err := r.bilic.GetLiveRoomInfo(p.roomId)
	durationRoomInfo := time.Since(startTimeRoomInfo)
	l.Debugf("duration: function=GetLiveRoomInfo spent=%v", durationRoomInfo)
	if err != nil {
		return err
	} else if roomInfo.IsEncrypted {
		return ErrRoomEncrypted
	} else if roomInfo.LockStatus != 0 {
		return ErrRoomBanned
	} else if roomInfo.LiveStatus != 1 {
		return ErrStreamNotLive
	}

	startTimeStreamURLs := time.Now()
	streams, err := r.bilic.GetStreamURLsV2(p.roomId, p.opts.streamOptions...)
	durationStreamURLs := time.Since(startTimeStreamURLs)
	l.Debugf("duration: function=GetStreamURLsV2 spent=%v", durationStreamURLs)

	if err != nil {
		return err
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

		ch, strategy, connected, err := r.connectStream(l, p.roomId, ctx, streamInfo, urlPreview, idx, len(streams))
		if err != nil {
			return err
		}
		if !connected {
			continue
		}

		if !r.sessionReadyForConfirm(p) {
			discardStreamCh(ch)
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
			discardStreamCh(ch)
			return err
		}

		return r.prepare(p.roomId, ch, strategy, info.ctx, info, p.mode == startModeUser)
	}

	l.Warn("no more url left")
	return ErrStreamURLsUnreachable
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
		if r.cfg.RecordingRecoveryDuration == "reset" {
			info.startTime = now
		}
		info.startOptions = snapshotStartOptions(p.opts)
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

	info := &Info{
		ctx:          p.ctx,
		cancel:       p.cancel,
		startOptions: snapshotStartOptions(p.opts),
		startTime:    now,
		room:         roomInfo,
		maxDuration:  maxDuration,
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
	})

	return info, nil
}

func (r *Service) connectStream(
	l *logrus.Entry,
	roomId int,
	ctx context.Context,
	streamInfo bilibili.StreamURLInfo,
	urlPreview string,
	idx, total int,
) (<-chan []byte, rs.StreamRecordStrategy, bool, error) {
	var ch <-chan []byte
	var strategy rs.StreamRecordStrategy

	switch streamInfo.Format {
	case "ts", "fmp4":
		l.Debugf("stream response [%d/%d]: protocol=%s, format=%s, codec=%s, qn=%d, req=%s",
			idx+1,
			total,
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			urlPreview,
		)

		initialURL := streamInfo.URL
		profile := utils.Ternary(
			streamInfo.Format == "ts",
			bilibili.ProfileHLSTS,
			bilibili.ProfileHLSFMP4,
		)

		fetchM3u8URL := func() (string, error) {
			if initialURL != "" {
				url := initialURL
				initialURL = ""
				return url, nil
			}

			latestStreams, fetchErr := r.bilic.GetStreamURLsV2(roomId, bilibili.WithProfiles(profile))
			if fetchErr != nil {
				return "", fetchErr
			} else if len(latestStreams) == 0 {
				return "", nil
			}

			tryResolve := func(candidate bilibili.StreamURLInfo) (string, bool) {
				fetchCtx := ctx
				if _, ok := fetchCtx.Deadline(); !ok {
					var cancel context.CancelFunc
					fetchCtx, cancel = context.WithTimeout(fetchCtx, 3*time.Second)
					defer cancel()
				}
				m3u8Resp, fetchErr := r.bilic.GetLiveHlsPlaylistClient().R().SetContext(fetchCtx).Get(candidate.URL)
				if fetchErr != nil {
					return "", false
				}
				if body := m3u8Resp.RawBody(); body != nil {
					defer body.Close()
				}
				if m3u8Resp.StatusCode() != 200 {
					return "", false
				}

				if m3u8Resp.RawResponse != nil && m3u8Resp.RawResponse.Request != nil && m3u8Resp.RawResponse.Request.URL != nil {
					return m3u8Resp.RawResponse.Request.URL.String(), true
				}
				return candidate.URL, true
			}

			for _, candidate := range latestStreams {
				if candidate.Format != streamInfo.Format || candidate.Protocol != streamInfo.Protocol || candidate.Codec != streamInfo.Codec {
					continue
				}
				if refreshedURL, ok := tryResolve(candidate); ok {
					return refreshedURL, nil
				}
			}

			for _, candidate := range latestStreams {
				if candidate.Format != streamInfo.Format {
					continue
				}
				if refreshedURL, ok := tryResolve(candidate); ok {
					return refreshedURL, nil
				}
			}

			return "", nil
		}

		hlsCh, hlsErr := r.st.ReadHlsStream(fetchM3u8URL, r.bilic.GetLiveHlsPlaylistClient(), r.bilic.GetLiveHlsSegmentClient(), ctx, streamInfo.Qn)
		if hlsErr != nil {
			if errors.Is(hlsErr, context.Canceled) || ctx.Err() != nil {
				return nil, nil, false, context.Canceled
			}
			l.Errorf("cannot start HLS stream: %v, will try next url", hlsErr)
			return nil, nil, false, nil
		}
		ch = hlsCh
		strategy = utils.TernaryFunc(
			streamInfo.Format == "ts",
			func() rs.StreamRecordStrategy { return rs.NewHlsTsStrategy(streamInfo.Qn) },
			func() rs.StreamRecordStrategy { return rs.NewHlsFmp4Strategy(streamInfo.Qn) },
		)
	case "flv":
		startTimeFlv := time.Now()
		resp, err := r.bilic.FetchLiveStreamUrlWithCtx(streamInfo.URL, ctx)
		durationFlv := time.Since(startTimeFlv)
		l.Debugf("duration: function=FetchLiveStreamUrlWithCtx spent=%v", durationFlv)

		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil, nil, false, context.Canceled
			}
			l.Errorf("cannot fetch url: %v, will try next url (protocol=%s, format=%s, codec=%s, qn=%d, url=%s)",
				err,
				streamInfo.Protocol,
				streamInfo.Format,
				streamInfo.Codec,
				streamInfo.Qn,
				urlPreview,
			)
			return nil, nil, false, nil
		} else if resp.StatusCode() != 200 {
			l.Errorf("non-200 response: %d, will try next url (protocol=%s, format=%s, codec=%s, qn=%d, url=%s)",
				resp.StatusCode(),
				streamInfo.Protocol,
				streamInfo.Format,
				streamInfo.Codec,
				streamInfo.Qn,
				urlPreview,
			)
			return nil, nil, false, nil
		}

		finalURL := ""
		if resp.RawResponse != nil && resp.RawResponse.Request != nil && resp.RawResponse.Request.URL != nil {
			finalURL = resp.RawResponse.Request.URL.String()
		}

		l.Debugf("stream response [%d/%d]: status=%d, content-type=%s, protocol=%s, format=%s, codec=%s, qn=%d, req=%s, final=%s",
			idx+1,
			total,
			resp.StatusCode(),
			resp.Header().Get("Content-Type"),
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			urlPreview,
			utils.TruncateString(finalURL, 160),
		)

		flvCh, flvErr := r.st.ReadFlvStream(resp, ctx, streamInfo.Qn)
		if flvErr != nil {
			l.Errorf("cannot capture url stream: %v, will try next url", flvErr)
			return nil, nil, false, nil
		}
		ch = flvCh
		strategy = rs.NewFlvStrategy(streamInfo.Qn)
	default:
		return nil, nil, false, fmt.Errorf("unsupported format: %s", streamInfo.Format)
	}

	return ch, strategy, true, nil
}

func discardStreamCh(ch <-chan []byte) {
	if ch == nil {
		return
	}
	go func() {
		for range ch {
		}
	}()
}
