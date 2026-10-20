package preview

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const copyBufSize = 32 * 1024

type Media struct {
	MIME         string
	Status       int
	ContentRange string
	Body         io.ReadCloser
}

func (s *Service) OpenMedia(ctx context.Context, roomID int, queryOff, queryLen int64, rangeHeader string) (Media, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	v, ok := s.roomView(roomID)
	if !ok || v.path == "" {
		return Media{}, ErrNotRecording
	}

	if queryLen > 0 {
		return s.openQueryRange(v, queryOff, queryLen)
	}
	return s.openHTTPRange(ctx, roomID, v, rangeHeader)
}

func (s *Service) openQueryRange(v roomView, off, length int64) (Media, error) {
	if off < v.previewStart {
		return Media{}, ErrSegmentMissing
	}
	st, err := os.Stat(v.path)
	if err != nil || st.Size() < off+length {
		return Media{}, ErrSegmentMissing
	}
	pr, pw := io.Pipe()
	go func() {
		_ = pw.CloseWithError(copyRange(pw, v.path, off, length))
	}()
	return Media{
		MIME:         mediaMIME(v.ext),
		Status:       http.StatusPartialContent,
		ContentRange: formatContentRange(off, off+length-1, st.Size()),
		Body:         pr,
	}, nil
}

func (s *Service) openHTTPRange(ctx context.Context, roomID int, v roomView, header string) (Media, error) {
	st, err := os.Stat(v.path)
	if err != nil {
		return Media{}, ErrSegmentMissing
	}
	start, end, follow, err := parseByteRange(header, st.Size())
	if err != nil {
		return Media{}, err
	}
	// FLV needs the file preamble and sequence headers at offset 0.
	// Clamping a late join to previewStart would start mid-file and be unplayable.
	if v.ext != ".flv" && start < v.previewStart {
		start = v.previewStart
	}
	if follow {
		pr, pw := io.Pipe()
		gen := v.gen
		path := v.path
		off := start
		var prefix []byte
		var baseTS uint32
		// Late join: skip the on-disk backlog so the player starts at the
		// live edge. Finite Range probes still read the real file bytes.
		if v.ext == ".flv" && start == 0 {
			if join, ok := flvLiveJoin(path); ok {
				prefix, off, baseTS = join.prefix, join.offset, join.baseTS
			}
		}
		go func() {
			_ = pw.CloseWithError(followFile(ctx, pw, path, off, prefix, baseTS, gen, func() uint64 {
				return s.generation(roomID)
			}))
		}()
		return Media{
			MIME:         mediaMIME(v.ext),
			Status:       http.StatusOK,
			ContentRange: "bytes " + strconv.FormatInt(start, 10) + "-/*",
			Body:         pr,
		}, nil
	}
	if end >= st.Size() {
		end = st.Size() - 1
	}
	if start > end || start < 0 {
		return Media{}, ErrRangeNotSatisfiable
	}
	length := end - start + 1
	pr, pw := io.Pipe()
	go func() {
		_ = pw.CloseWithError(copyRange(pw, v.path, start, length))
	}()
	return Media{
		MIME:         mediaMIME(v.ext),
		Status:       http.StatusPartialContent,
		ContentRange: formatContentRange(start, end, st.Size()),
		Body:         pr,
	}, nil
}

func copyRange(w io.Writer, path string, offset, length int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	buf := make([]byte, copyBufSize)
	_, err = io.CopyBuffer(w, io.LimitReader(f, length), buf)
	return err
}

func followFile(ctx context.Context, w io.Writer, path string, offset int64, prefix []byte, baseTS uint32, gen uint64, generation func() uint64) error {
	if len(prefix) > 0 {
		if _, err := w.Write(prefix); err != nil {
			return err
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	dst := io.Writer(w)
	if baseTS > 0 {
		dst = &tsRebaser{dst: w, base: baseTS}
	}
	buf := make([]byte, copyBufSize)
	pos := offset
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if generation != nil && generation() != gen {
			return nil
		}
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if st.Size() > pos {
			n, copyErr := io.CopyBuffer(dst, io.LimitReader(f, st.Size()-pos), buf)
			pos += n
			if copyErr != nil {
				return copyErr
			}
			continue
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func parseByteRange(header string, size int64) (start, end int64, follow bool, err error) {
	if header == "" {
		return 0, 0, true, nil
	}
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false, ErrRangeNotSatisfiable
	}
	parts := strings.SplitN(strings.TrimPrefix(header, "bytes="), "-", 2)
	if len(parts) != 2 || parts[0] == "" {
		return 0, 0, false, ErrRangeNotSatisfiable
	}
	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false, ErrRangeNotSatisfiable
	}
	if parts[1] == "" {
		return start, 0, true, nil
	}
	end, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil || end < start {
		return 0, 0, false, ErrRangeNotSatisfiable
	}
	if size == 0 {
		return 0, 0, false, ErrRangeNotSatisfiable
	}
	return start, end, false, nil
}

func formatContentRange(start, end, total int64) string {
	return "bytes " + strconv.FormatInt(start, 10) + "-" + strconv.FormatInt(end, 10) + "/" + strconv.FormatInt(total, 10)
}

func mediaMIME(ext string) string {
	switch strings.ToLower(ext) {
	case ".flv":
		return "video/x-flv"
	case ".ts":
		return "video/mp2t"
	case ".fmp4":
		return "video/mp4"
	default:
		return "application/octet-stream"
	}
}
