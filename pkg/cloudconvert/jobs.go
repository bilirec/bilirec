package cloudconvert

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (c *Client) GetJobDetails(jobID string, includes ...string) (*JobResponse, error) {
	if jobID == "" {
		return nil, fmt.Errorf("job id is required")
	}

	req := c.client.R().
		SetContext(c.ctx).
		SetPathParam("id", jobID)

	if len(includes) > 0 {
		req.SetQueryParam("include", strings.Join(includes, ","))
	}

	res, err := req.Get("/jobs/{id}")
	if err != nil {
		return nil, err
	}
	if res.StatusCode() < 200 || res.StatusCode() >= 400 {
		return nil, fmt.Errorf("get job details failed with status code %d: %s", res.StatusCode(), res.String())
	}

	var jobRes JobResponse
	if err := json.Unmarshal(res.Body(), &jobRes); err != nil {
		return nil, err
	}

	return &jobRes, nil
}
