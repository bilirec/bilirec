package stream

import (
	"context"
	"errors"
	"fmt"

	"github.com/bilirec/bilirec/pkg/hls"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/go-resty/resty/v2"
)

// Re-export HLS stream errors for existing callers.
var (
	ErrM3u8Expired = hls.ErrM3u8Expired
	ErrNoM3u8URL   = hls.ErrNoM3u8URL
)

// ReadHlsStream polls an HLS m3u8 playlist and delivers each new segment as a
// complete []byte to the returned channel. One send = one full TS or fMP4
// segment, ready to be written to disk.
//
// Assembly lives here: PlaylistSession + InitSettle + StreamRunner.
// Protocol pieces are in pkg/hls; this method wires bilirec pool/logger/qn.
func (r *Service) ReadHlsStream(
	fetchM3u8URL func() (string, error),
	playlistClient, segmentClient *resty.Client,
	ctx context.Context,
	qn int,
	chunkPool *pool.BucketedBytesPool,
	releasePool func(),
) (<-chan []byte, error) {
	readBody := hls.PoolSegmentBodyReader(chunkPool)
	release := hls.PoolBytesReleaser(chunkPool)

	settle := &hls.InitSettle{
		Release: release,
		Log:     logger,
	}

	session, err := hls.NewPlaylistSession(ctx, hls.PlaylistSessionOptions{
		FetchURL:       fetchM3u8URL,
		PlaylistClient: playlistClient,
		SegmentClient:  segmentClient,
		ReadBody:       readBody,
		Log:            logger,
		OnURLRefresh: func() {
			settle.Reset("m3u8 URL 已刷新")
		},
	})
	if err != nil {
		return nil, err
	}

	if err := session.RefreshURL("initial"); err != nil {
		if errors.Is(err, hls.ErrNoM3u8URL) {
			return nil, ErrNoM3u8URL
		}
		return nil, fmt.Errorf("hls：无法获取初始 m3u8 URL：%w", err)
	}

	pl, err := session.FetchWithRefresh()
	if err != nil {
		if errors.Is(err, hls.ErrNoM3u8URL) {
			return nil, ErrNoM3u8URL
		}
		return nil, fmt.Errorf("hls：无法解析初始 m3u8：%w", err)
	}

	mediaSeq, segs := pl.MediaSeq, pl.Segments
	pollInterval := hls.DerivePollInterval(pl)
	logger.Infof("hls：轮询间隔=%v（target=%.2fs，first-extinf=%.2fs）", pollInterval, func() float64 {
		if len(segs) > 0 {
			return pl.TargetDuration
		}
		return 0
	}(), func() float64 {
		if len(segs) > 0 {
			return segs[0].Duration
		}
		return 0
	}())

	nextSeq := mediaSeq + int64(len(segs))
	if pl.MapURI != "" {
		nextSeq = mediaSeq
	}

	return hls.Start(ctx, hls.StreamRunnerOptions{
		Session:         session,
		Settle:          settle,
		SegmentClient:   segmentClient,
		ReadBody:        readBody,
		ReleaseBytes:    release,
		Log:             logger,
		ChanBufferSize:  r.chanBufferSizeForQn(qn),
		OnClose:         releasePool,
		InitialPlaylist: pl,
		PollInterval:    pollInterval,
		NextSeq:         nextSeq,
		PrevBaseSeq:     mediaSeq,
	})
}
