package record

import (
	"strconv"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/services/recorder"
	"github.com/gofiber/fiber/v3"
)

const durationMinutesNotProvided = 0

type resolvedStartParams struct {
	durationMinutes        int
	hasStreamProfile       bool
	streamProfileRaw       string
	streamProfilesFromJSON []string
	hasQn                  bool
	qn                     int
	onlyAudio              bool
	recordDanmaku          bool
	deleteOldestOnLowDisk  bool
}

func parseStartRecordingParams(ctx fiber.Ctx) (resolvedStartParams, error) {
	if !ctx.HasBody() {
		return resolvedFromStartRecordingQuery(ctx)
	}
	var req StartRecordingRequest
	if err := ctx.Bind().Body(&req); err != nil {
		return resolvedStartParams{}, fiber.NewError(fiber.StatusBadRequest, "无效的请求数据")
	}
	return resolvedFromStartRecordingRequest(req), nil
}

func resolvedFromStartRecordingQuery(ctx fiber.Ctx) (resolvedStartParams, error) {
	p := resolvedStartParams{
		durationMinutes:  fiber.Query(ctx, "duration_minutes", durationMinutesNotProvided),
		streamProfileRaw: strings.TrimSpace(fiber.Query(ctx, "stream_profile", "")),
	}
	p.hasStreamProfile = p.streamProfileRaw != ""

	qnRaw := strings.TrimSpace(fiber.Query(ctx, "qn", ""))
	if qnRaw != "" {
		qn, err := strconv.Atoi(qnRaw)
		if err != nil {
			return resolvedStartParams{}, fiber.NewError(fiber.StatusBadRequest, "无效的 qn 参数")
		}
		p.hasQn = true
		p.qn = qn
	}

	onlyAudioRaw := strings.TrimSpace(strings.ToLower(fiber.Query(ctx, "only_audio", "false")))
	p.onlyAudio, _ = strconv.ParseBool(onlyAudioRaw)

	recordDanmakuRaw := strings.TrimSpace(strings.ToLower(fiber.Query(ctx, "record_danmaku", "false")))
	p.recordDanmaku, _ = strconv.ParseBool(recordDanmakuRaw)

	deleteOldestRaw := strings.TrimSpace(strings.ToLower(fiber.Query(ctx, "delete_oldest_on_low_disk", "false")))
	p.deleteOldestOnLowDisk, _ = strconv.ParseBool(deleteOldestRaw)

	return p, nil
}

func resolvedFromStartRecordingRequest(req StartRecordingRequest) resolvedStartParams {
	p := resolvedStartParams{}
	if req.DurationMinutes != nil {
		p.durationMinutes = *req.DurationMinutes
	}
	if req.Qn != nil {
		p.hasQn = true
		p.qn = *req.Qn
	}
	if req.OnlyAudio != nil {
		p.onlyAudio = *req.OnlyAudio
	}
	if req.RecordDanmaku != nil {
		p.recordDanmaku = *req.RecordDanmaku
	}
	if req.DeleteOldestOnLowDisk != nil {
		p.deleteOldestOnLowDisk = *req.DeleteOldestOnLowDisk
	}
	switch {
	case req.StreamProfiles != nil:
		p.hasStreamProfile = true
		p.streamProfilesFromJSON = *req.StreamProfiles
	case req.StreamProfile != nil:
		p.hasStreamProfile = true
		p.streamProfileRaw = strings.TrimSpace(*req.StreamProfile)
	}
	return p
}

func recordStartOptionsFromResolved(p resolvedStartParams) ([]recorder.RecordStartOption, error) {
	var startArgs []recorder.RecordStartOption
	switch {
	case p.durationMinutes == -1:
		startArgs = []recorder.RecordStartOption{recorder.WithDuration(0)}
	case p.durationMinutes > 0:
		startArgs = []recorder.RecordStartOption{recorder.WithDuration(time.Duration(p.durationMinutes) * time.Minute)}
	}

	streamOptions := []bilibili.GetStreamURLsOption{}
	if p.hasStreamProfile {
		var profiles []bilibili.StreamProfile
		var parseErr error
		if p.streamProfilesFromJSON != nil {
			profiles, parseErr = bilibili.NormalizeStreamProfiles(p.streamProfilesFromJSON)
			if parseErr != nil {
				return nil, fiber.NewError(fiber.StatusBadRequest, "无效的 stream_profiles 参数")
			}
		} else if p.streamProfileRaw != "" {
			profiles, parseErr = bilibili.ParseStreamProfiles(p.streamProfileRaw)
			if parseErr != nil {
				return nil, fiber.NewError(fiber.StatusBadRequest, "无效的 stream_profile 参数")
			}
		}
		if len(profiles) > 0 {
			streamOptions = append(streamOptions, bilibili.WithProfiles(profiles...))
		}
	}

	if p.hasQn {
		streamOptions = append(streamOptions, bilibili.WithQn(bilibili.Quality(p.qn)))
	}
	if p.onlyAudio {
		streamOptions = append(streamOptions, bilibili.WithOnlyAudio(true))
	}

	startArgs = append(startArgs, recorder.WithStreamOptions(streamOptions...))

	if p.recordDanmaku {
		startArgs = append(startArgs, recorder.WithRecordDanmaku(true))
	}
	if p.deleteOldestOnLowDisk {
		startArgs = append(startArgs, recorder.WithDeleteOldestOnLowDisk(true))
	}

	return startArgs, nil
}
