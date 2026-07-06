package bilibili

import (
	"context"
	"encoding/json"
	"fmt"

	bili "github.com/CuteReimu/bilibili/v2"
	"github.com/bilirec/bilirec/pkg/fallback"
	"github.com/go-resty/resty/v2"
	"github.com/pkg/errors"
)

var (
	ErrRoomNotFound        = errors.New("房间不存在")
	ErrStreamGeoRestricted = errors.New("该直播间在当前地区不可用")
	ErrNilResponse         = errors.New("nil response")
)

// APIError is the unified Bilibili JSON envelope error for bilirec.
// It replaces direct use of bili.Error from the embedded SDK client.
// Transport-level resty errors are not wrapped as APIError.
type APIError struct {
	HTTPStatus int
	Code       int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("bilibili api error: %s (http=%d, code=%d)", e.Message, e.HTTPStatus, e.Code)
	}
	return fmt.Sprintf("bilibili api error (http=%d, code=%d)", e.HTTPStatus, e.Code)
}

type apiEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// AsAPIError unwraps bilirec APIError or converts bili.Error from the embedded SDK.
func AsAPIError(err error) (*APIError, bool) {
	if err == nil {
		return nil, false
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	if biliErr, ok := errors.Cause(err).(bili.Error); ok {
		return &APIError{Code: biliErr.Code, Message: biliErr.Message}, true
	}
	return nil, false
}

// ParseAPIError reads the standard Bilibili JSON envelope from a resty response.
func ParseAPIError(resp *resty.Response) (*APIError, error) {
	if resp == nil {
		return nil, ErrNilResponse
	}
	var env apiEnvelope
	if err := json.Unmarshal(resp.Body(), &env); err != nil {
		return nil, err
	}
	return &APIError{
		HTTPStatus: resp.StatusCode(),
		Code:       env.Code,
		Message:    env.Message,
	}, nil
}

func isRiskControl(code int) bool {
	if code == 0 || code == 1 {
		return false
	}
	if code == 400 || code == 404 || code >= 500 {
		return false
	}
	return true
}

func IsErrRoomNotFound(err error) bool {
	if err == ErrRoomNotFound {
		return true
	}
	if apiErr, ok := AsAPIError(err); ok && apiErr.Code == 1 {
		return true
	}
	return false
}

func IsErrStreamGeoRestricted(err error) bool {
	return errors.Is(err, ErrStreamGeoRestricted)
}

// liveInterpret is the fallback interpreter for live room info requests.
func liveInterpret(ctx context.Context, resp *resty.Response, err error) fallback.Decision {
	if err != nil {
		return fallback.DecisionAbort
	}
	apiErr, parseErr := ParseAPIError(resp)
	if parseErr != nil {
		return fallback.DecisionAbort
	}
	if apiErr.Code == 0 {
		return fallback.DecisionOK
	}
	if isRiskControl(apiErr.Code) {
		return fallback.DecisionFallback
	}
	return fallback.DecisionAbort
}
