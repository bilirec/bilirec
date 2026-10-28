package stream

import (
	"context"
	"errors"
	"time"

	"github.com/bilirec/bilirec/utils"
)

func connectFLV(svc *Service, ctx context.Context, req ConnectRequest) (ConnectResult, error) {
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

	startTimeFlv := time.Now()
	resp, fetchErr := svc.bilic.FetchLiveStreamUrlWithCtx(streamInfo.URL, ctx)
	l.Debugf("duration: function=FetchLiveStreamUrlWithCtx spent=%v", time.Since(startTimeFlv))

	if fetchErr != nil {
		if errors.Is(fetchErr, context.Canceled) || ctx.Err() != nil {
			return out, context.Canceled
		}
		l.Errorf("cannot fetch url: %v, will try next url (protocol=%s, format=%s, codec=%s, qn=%d, url=%s)",
			fetchErr,
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			req.URLPreview,
		)
		return out, nil
	}
	if resp.StatusCode() != 200 {
		drainAndCloseBody(resp.RawBody())
		l.Errorf("non-200 response: %d, will try next url (protocol=%s, format=%s, codec=%s, qn=%d, url=%s)",
			resp.StatusCode(),
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			req.URLPreview,
		)
		return out, nil
	}

	finalURL := ""
	if resp.RawResponse != nil && resp.RawResponse.Request != nil && resp.RawResponse.Request.URL != nil {
		finalURL = resp.RawResponse.Request.URL.String()
	}

	l.Debugf("stream response [%d/%d]: status=%d, content-type=%s, protocol=%s, format=%s, codec=%s, qn=%d, req=%s, final=%s",
		req.Attempt, req.Total,
		resp.StatusCode(),
		resp.Header().Get("Content-Type"),
		streamInfo.Protocol,
		streamInfo.Format,
		streamInfo.Codec,
		streamInfo.Qn,
		req.URLPreview,
		utils.TruncateString(finalURL, 160),
	)

	flvCh, flvErr := svc.ReadFlvStream(resp, ctx, streamInfo.Qn, chunkPool, releaseChunkPool)
	if flvErr != nil {
		drainAndCloseBody(resp.RawBody())
		l.Errorf("cannot capture url stream: %v, will try next url", flvErr)
		return out, nil
	}

	out.Ch = flvCh
	out.ChunkPool = chunkPool
	out.Connected = true
	connected = true
	return out, nil
}
