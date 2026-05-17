package cloudconvert

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
)

func NewImportURLTask(name string, payload *ImportURLRequest) *JobCreateTask {
	return &JobCreateTask{
		Name:      name,
		Operation: "import/url",
		Payload:   payload,
	}
}

func NewImportUploadTask(name string, payload *ImportUploadRequest) *JobCreateTask {
	return &JobCreateTask{
		Name:      name,
		Operation: "import/upload",
		Payload:   payload,
	}
}

func NewImportS3Task(name string, payload *ImportS3Request) *JobCreateTask {
	return &JobCreateTask{
		Name:      name,
		Operation: "import/s3",
		Payload:   payload,
	}
}

func (c *Client) CreateUploadTask(redirect ...string) (*ImportUploadResponse, error) {
	req := c.client.R().SetContext(c.ctx)

	if len(redirect) > 0 {
		req.SetBody(&ImportUploadRequest{Redirect: redirect[0]})
	}

	res, err := req.Post("/import/upload")
	if err != nil {
		return nil, err
	}

	var taskRes ImportUploadResponse
	if err := json.Unmarshal(res.Body(), &taskRes); err != nil {
		return nil, err
	} else if res.StatusCode() < 200 || res.StatusCode() >= 400 {
		return nil, fmt.Errorf("视频转换失败，状态码 %d：%s", res.StatusCode(), res.String())
	}
	return &taskRes, nil
}

// UploadFileToTask uploads the given file to the specified import upload task.
// This function will use net/http instead of resty due to the need of streaming upload with pipe.
// It is because `resty@v2` will buffer the entire request body in memory which cause OOM for large files.
func (c *Client) UploadFileToTask(f *os.File, form *ImportUploadForm) error {
	defer f.Close()
	// Ensure file offset at beginning
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	// Writer goroutine: writes fields and stream-copies the file into the multipart writer.
	go func() {
		defer pw.Close()
		defer mw.Close()

		// write form fields
		for k, v := range form.Parameters {
			if err := mw.WriteField(k, fmt.Sprint(v)); err != nil {
				pw.CloseWithError(err)
				return
			}
		}

		part, err := mw.CreateFormFile("file", f.Name())
		if err != nil {
			pw.CloseWithError(err)
			return
		}

		buf := c.uploadPool.GetBytes()
		defer c.uploadPool.PutBytes(buf)

		if _, err := io.CopyBuffer(part, f, buf); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	// Create HTTP request using standard library (NOT resty)
	httpReq, err := http.NewRequestWithContext(c.ctx, "POST", form.URL, pr)
	if err != nil {
		return fmt.Errorf("创建请求失败：%w", err)
	}

	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	res, err := c.streamClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("上传请求失败：%w", err)
	}
	defer res.Body.Close()

	// Check response status
	if res.StatusCode < 200 || res.StatusCode >= 400 {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("上传失败，状态码 %d：%s", res.StatusCode, string(body))
	}

	return nil
}
