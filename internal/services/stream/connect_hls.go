package stream

import (
	"context"
	"errors"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

func connectHLS(
	svc *Service,
	ctx context.Context,
	req ConnectRequest,
	profile bilibili.StreamProfile,
) (ConnectResult, error) {
	var out ConnectResult
	streamInfo := req.StreamInfo
	l := req.Log

	chunkPool, releaseChunkPool := svc.AcquireChunkPool(streamInfo.Qn)
	connected := false
	defer func() {
		if !connected {
			releaseChunkPool()
		}
	}()

	l.Debugf("stream response [%d/%d]: protocol=%s, format=%s, codec=%s, qn=%d, req=%s",
		req.Attempt, req.Total,
		streamInfo.Protocol,
		streamInfo.Format,
		streamInfo.Codec,
		streamInfo.Qn,
		req.URLPreview,
	)

	fetchM3u8URL := hlsFetchM3u8URL(ctx, svc.bilic, req.RoomID, streamInfo, profile)
	hlsCh, hlsErr := svc.ReadHlsStream(
		req.RoomID,
		req.SessionMeta,
		fetchM3u8URL,
		svc.bilic.GetLiveHlsPlaylistClient(),
		svc.bilic.GetLiveHlsSegmentClient(),
		ctx,
		streamInfo.Qn,
		chunkPool,
		releaseChunkPool,
	)
	if hlsErr != nil {
		if errors.Is(hlsErr, context.Canceled) || ctx.Err() != nil {
			return out, context.Canceled
		}
		l.Errorf("cannot start HLS stream: %v, will try next url", hlsErr)
		return out, nil
	}

	out.Ch = hlsCh
	out.ChunkPool = chunkPool
	out.Connected = true
	connected = true
	return out, nil
}

func hlsFetchM3u8URL(
	ctx context.Context,
	bilic *bilibili.Client,
	roomID int,
	streamInfo bilibili.StreamURLInfo,
	profile bilibili.StreamProfile,
) func() (string, error) {
	initialURL := streamInfo.URL
	return func() (string, error) {
		if initialURL != "" {
			url := initialURL
			initialURL = ""
			return url, nil
		}

		latestStreams, fetchErr := bilic.GetStreamURLsV2(roomID,
			bilibili.WithProfiles(profile),
			bilibili.WithQn(bilibili.Quality(streamInfo.Qn)),
			bilibili.WithOnlyAudio(streamInfo.IsAudioOnly),
		)
		if fetchErr != nil {
			return "", fetchErr
		}
		if len(latestStreams) == 0 {
			return "", nil
		}

		tryResolve := func(candidate bilibili.StreamURLInfo) (string, bool) {
			fetchCtx := ctx
			if _, ok := fetchCtx.Deadline(); !ok {
				var cancel context.CancelFunc
				fetchCtx, cancel = context.WithTimeout(fetchCtx, 3*time.Second)
				defer cancel()
			}
			m3u8Resp, getErr := bilic.GetLiveHlsPlaylistClient().R().SetContext(fetchCtx).Get(candidate.URL)
			if getErr != nil {
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
}
