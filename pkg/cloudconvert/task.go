package cloudconvert

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var ErrTaskNotFound = errors.New("任务未找到")

func (c *Client) GetTask(taskID string, includes ...string) (*TaskResponse, error) {
	req := c.client.R().
		SetContext(c.ctx).
		SetQueryParam("include", strings.Join(includes, ",")).
		SetPathParam("id", taskID)

	res, err := req.Get("/tasks/{id}")
	if err != nil {
		return nil, err
	}
	if res.StatusCode() == 404 {
		return nil, ErrTaskNotFound
	}

	var taskRes TaskResponse
	if err := json.Unmarshal(res.Body(), &taskRes); err != nil {
		return nil, err
	}

	return &taskRes, nil
}

func (c *Client) ListTasks(f *TaskListFilter) (*TaskListResponse, error) {
	req := c.client.R().SetContext(c.ctx)

	if f.JobID != "" {
		req.SetQueryParam("filter[job_id]", f.JobID)
	}
	if f.Status != "" {
		req.SetQueryParam("filter[status]", f.Status)
	}
	if f.Operation != "" {
		req.SetQueryParam("filter[operation]", f.Operation)
	}
	if len(f.Include) > 0 {
		req.SetQueryParam("include", strings.Join(f.Include, ","))
	}
	if f.PerPage > 0 {
		req.SetQueryParam("per_page", strconv.Itoa(f.PerPage))
	}
	if f.Page > 0 {
		req.SetQueryParam("page", strconv.Itoa(f.Page))
	}

	res, err := req.Get("/tasks")
	if err != nil {
		return nil, err
	}
	var taskListRes TaskListResponse
	if err := json.Unmarshal(res.Body(), &taskListRes); err != nil {
		return nil, err
	}
	return &taskListRes, nil
}

func (c *Client) CancelTask(taskID string) error {
	req := c.client.R().
		SetContext(c.ctx).
		SetPathParam("id", taskID)
	res, err := req.Post("/tasks/{id}/cancel")
	if err != nil {
		return err
	}
	if res.StatusCode() != 200 && res.StatusCode() != 204 {
		return fmt.Errorf("%d: %s", res.StatusCode(), res.String())
	}
	return nil
}

func (c *Client) RetryTask(taskID string) (bool, error) {
	req := c.client.R().
		SetContext(c.ctx).
		SetPathParam("id", taskID)
	res, err := req.Post("/tasks/{id}/retry")
	if err != nil {
		return false, err
	}
	switch res.StatusCode() {
	case 200, 204:
		return true, nil
	case 404:
		return false, ErrTaskNotFound
	default:
		return false, fmt.Errorf("%d: %s", res.StatusCode(), res.String())
	}
}

func (c *Client) DeleteTask(taskID string) (bool, error) {
	req := c.client.R().
		SetContext(c.ctx).
		SetPathParam("id", taskID)
	res, err := req.Delete("/tasks/{id}")
	if err != nil {
		return false, err
	}
	switch res.StatusCode() {
	case 200, 204:
		return true, nil
	case 404:
		return false, ErrTaskNotFound
	default:
		return false, fmt.Errorf("%d: %s", res.StatusCode(), res.String())
	}
}
