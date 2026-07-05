package stream

import (
	"fmt"
	"io"

	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/go-resty/resty/v2"
)

const hlsSegmentBodyInitialWant = 256 * 1024

func readHlsSegmentBody(chunkPool *pool.BucketedBytesPool, resp *resty.Response) ([]byte, error) {
	body := resp.RawBody()
	if body == nil {
		return nil, fmt.Errorf("hls：响应体为空")
	}
	defer body.Close()

	want := hlsSegmentBodyInitialWant
	if resp.RawResponse != nil && resp.RawResponse.ContentLength > 0 {
		want = int(resp.RawResponse.ContentLength)
	}
	buf := chunkPool.GetSized(want)

	n := 0
	for {
		if n >= cap(buf) {
			nextWant := cap(buf) * 2
			bigger := chunkPool.GetSized(nextWant)
			copy(bigger, buf[:n])
			chunkPool.Put(buf[:cap(buf)])
			buf = bigger
		}
		nn, err := body.Read(buf[n:cap(buf)])
		n += nn
		if err == io.EOF {
			break
		}
		if err != nil {
			chunkPool.Put(buf[:cap(buf)])
			return nil, fmt.Errorf("读取分片失败：%w", err)
		}
	}
	return buf[:n], nil
}
