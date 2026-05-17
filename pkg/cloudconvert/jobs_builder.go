package cloudconvert

import (
	"encoding/json"
	"fmt"
	"maps"
)

type JobBuilder struct {
	client *Client
	tag    string
	tasks  []*JobCreateTask
}



func (c *Client) NewJobBuilder() *JobBuilder {
	return &JobBuilder{client: c}
}

func (b *JobBuilder) SetTag(tag string) *JobBuilder {
	b.tag = tag
	return b
}

func (b *JobBuilder) AddTask(task *JobCreateTask) *JobBuilder {
	if task == nil {
		return b
	}
	b.tasks = append(b.tasks, task)
	return b
}

func (b *JobBuilder) Submit() (*JobSubmitResult, error) {
	if len(b.tasks) == 0 {
		return nil, fmt.Errorf("未添加任何任务")
	}

	payload := map[string]any{
		"tasks": map[string]any{},
	}
	if b.tag != "" {
		payload["tag"] = b.tag
	}

	tasksMap := payload["tasks"].(map[string]any)
	for _, task := range b.tasks {
		if task.Name == "" {
			return nil, fmt.Errorf("任务名称为必填项")
		}
		if task.Operation == "" {
			return nil, fmt.Errorf("任务 %s 的 operation 为必填项", task.Name)
		}

		taskMap := map[string]any{
			"operation": task.Operation,
		}
		if len(task.DependsOnTasks) > 0 {
			taskMap["depends_on_tasks"] = task.DependsOnTasks
		}
		if task.Payload != nil {
			v, err := payloadToMap(task.Payload)
			if err != nil {
				return nil, fmt.Errorf("任务 %s 的 payload 错误：%w", task.Name, err)
			}
			maps.Copy(taskMap, v)
		}
		tasksMap[task.Name] = taskMap
	}

	res, err := b.client.client.R().
		SetContext(b.client.ctx).
		SetBody(payload).
		Post("/jobs")
	if err != nil {
		return nil, err
	}
	if res.StatusCode() < 200 || res.StatusCode() >= 400 {
		return nil, fmt.Errorf("创建 job 失败，状态码 %d：%s", res.StatusCode(), res.String())
	}

	var jobRes JobResponse
	if err := json.Unmarshal(res.Body(), &jobRes); err != nil {
		return nil, err
	}

	result := &JobSubmitResult{
		Job:   &jobRes,
		Tasks: make(map[string]*TaskData),
	}

	for _, task := range jobRes.Data.Tasks {
		if task.Name == "" {
			continue
		}
		result.Tasks[task.Name] = &task
	}

	return result, nil
}


func payloadToMap(payload any) (map[string]any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any)
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}
