package cloudconvert

import (
	"encoding/json"
	"fmt"
	"io"
)

func NewExportURLTask(name string, payload *ExportURLRequest) *JobCreateTask {
	return &JobCreateTask{
		Name:      name,
		Operation: "export/url",
		Payload:   payload,
	}
}

func (c *Client) CreateExportURL(payload *ExportURLRequest) (*TaskResponse, error) {
	req := c.client.R().
		SetContext(c.ctx).
		SetBody(payload)

	res, err := req.Post("/export/url")
	if err != nil {
		return nil, err
	}

	var exportRes TaskResponse
	if err := json.Unmarshal(res.Body(), &exportRes); err != nil {
		return nil, err
	} else if res.StatusCode() < 200 || res.StatusCode() >= 400 {
		return nil, fmt.Errorf("视频转换失败，状态码 %d：%s", res.StatusCode(), res.String())
	}

	return &exportRes, nil
}

func (c *Client) DownloadAsFileStream(url string) (io.ReadCloser, error) {
	res, err := c.streamClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("下载请求失败：%w", err)
	} else if res.StatusCode < 200 || res.StatusCode >= 400 {
		res.Body.Close()
		return nil, fmt.Errorf("下载失败，状态码 %d", res.StatusCode)
	}

	return res.Body, nil
}
