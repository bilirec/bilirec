package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type listItemParser[T any] func(any) (T, error)

// ParseDelimitedList parses a list from request body first, then falls back to query params.
// Supported body payloads:
// - [1,2,3]
// - {"items":[1,2,3]}
// - {"items":"1,2,3"}
func ParseDelimitedList[T any](paramName string, queryValue string, body []byte, bodyKeys []string, parseItem listItemParser[T]) ([]T, error) {
	if values, handled, err := parseListFromBody(paramName, body, bodyKeys, parseItem); err != nil {
		return nil, err
	} else if handled {
		return values, nil
	}

	return parseListFromQuery(paramName, queryValue, parseItem)
}

// ParseIntList is a convenience wrapper for comma-delimited int lists.
func ParseIntList(paramName string, queryValue string, body []byte, bodyKeys ...string) ([]int, error) {
	return ParseDelimitedList(paramName, queryValue, body, bodyKeys, parseIntListItem)
}

func parseListFromBody[T any](paramName string, body []byte, bodyKeys []string, parseItem listItemParser[T]) ([]T, bool, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return nil, false, nil
	}

	var payload any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, false, fmt.Errorf("无效的 JSON 请求体")
	}

	switch value := payload.(type) {
	case []any:
		items, err := parseListItems(value, parseItem)
		if err != nil {
			return nil, true, err
		}
		if len(items) == 0 {
			return nil, true, fmt.Errorf("缺少 %s 请求内容", paramName)
		}
		return items, true, nil
	case map[string]any:
		for _, key := range bodyKeys {
			if raw, ok := value[key]; ok {
				items, err := parseListValue(paramName, raw, parseItem)
				if err != nil {
					return nil, true, err
				}
				if len(items) == 0 {
					return nil, true, fmt.Errorf("缺少 %s 请求内容", paramName)
				}
				return items, true, nil
			}
		}
		return nil, false, nil
	default:
		items, err := parseListValue(paramName, value, parseItem)
		if err != nil {
			return nil, true, err
		}
		if len(items) == 0 {
			return nil, true, fmt.Errorf("缺少 %s 请求内容", paramName)
		}
		return items, true, nil
	}
}

func parseListFromQuery[T any](paramName string, queryValue string, parseItem listItemParser[T]) ([]T, error) {
	queryValue = strings.TrimSpace(queryValue)
	if queryValue == "" {
		return nil, fmt.Errorf("缺少 %s 查询参数或请求内容", paramName)
	}

	items, err := parseListValue(paramName, queryValue, parseItem)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("缺少 %s 查询参数或请求内容", paramName)
	}
	return items, nil
}

func parseListValue[T any](paramName string, raw any, parseItem listItemParser[T]) ([]T, error) {
	switch value := raw.(type) {
	case string:
		parts := SplitAndTrim(value, ",")
		if len(parts) == 0 {
			return nil, fmt.Errorf("缺少 %s 查询参数或请求内容", paramName)
		}
		items := make([]T, 0, len(parts))
		for _, part := range parts {
			item, err := parseItem(part)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	case []any:
		return parseListItems(value, parseItem)
	default:
		item, err := parseItem(value)
		if err != nil {
			return nil, err
		}
		return []T{item}, nil
	}
}

func parseListItems[T any](values []any, parseItem listItemParser[T]) ([]T, error) {
	items := make([]T, 0, len(values))
	for _, value := range values {
		item, err := parseItem(value)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func parseIntListItem(raw any) (int, error) {
	switch value := raw.(type) {
	case json.Number:
		id, err := value.Int64()
		if err != nil {
			return 0, fmt.Errorf("无效的数值: %s", value.String())
		}
		return int(id), nil
	case float64:
		if value != float64(int(value)) {
			return 0, fmt.Errorf("无效的数值: %v", value)
		}
		return int(value), nil
	case int:
		return value, nil
	case string:
		id, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("无效的数值: %s", value)
		}
		return id, nil
	default:
		return 0, fmt.Errorf("无效的数值: %v", value)
	}
}
