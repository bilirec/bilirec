package stream

import (
	"context"
	"fmt"
	"io"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/metrics"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pool"
)

// ConnectRequest parameters for TryConnect.
type ConnectRequest struct {
	RoomID      int
	SessionMeta metrics.StreamSessionMeta
	StreamInfo  bilibili.StreamURLInfo
	Log         logger.Logger
	Attempt     int
	Total       int
	URLPreview  string
}

// ConnectResult is returned when ingest is established for one URL candidate.
type ConnectResult struct {
	Ch        <-chan []byte
	ChunkPool *pool.BucketedBytesPool
	Connected bool
}

// TryConnect establishes ingest for one stream URL candidate.
// When Connected is false and err is nil, caller should try the next URL.
func (s *Service) TryConnect(ctx context.Context, req ConnectRequest) (ConnectResult, error) {
	var out ConnectResult

	switch req.StreamInfo.Format {
	case "flv":
		return connectFLV(s, ctx, req)
	case "ts":
		return connectHLS(s, ctx, req, bilibili.ProfileHLSTS)
	case "fmp4":
		return connectHLS(s, ctx, req, bilibili.ProfileHLSFMP4)
	default:
		return out, fmt.Errorf("unsupported format: %s", req.StreamInfo.Format)
	}
}

func drainAndCloseBody(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}
