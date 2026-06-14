package bilibili

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/testutil"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestGetStreamURLsV2WithDolbyQn(t *testing.T) {
	var client *Client
	app := fxtest.New(t,
		config.Module,
		Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)

	app.RequireStart()
	defer app.RequireStop()

	roomID := testutil.LiveRoomID(t)

	quality := QualityDolby

	urls, err := client.GetStreamURLsV2(roomID, WithQn(quality))
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) == 0 {
		t.Skipf("no streams url for room %d", roomID)
	}
	t.Logf("fetching stream using quality=%d (%s)", quality, quality.String())
	for i, stream := range urls {
		// log only
		t.Logf("%d. got quality=%d (%s)", i+1, stream.Qn, Quality(stream.Qn).String())
	}
}

func TestGetStreamUrlsV2OnlyAudioManyRooms(t *testing.T) {
	var client *Client
	app := fxtest.New(t,
		config.Module,
		Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)

	app.RequireStart()
	defer app.RequireStop()

	roomIDs := testutil.LiveRoomIDs(t, 10)
	if len(roomIDs) == 0 {
		t.Skip("no live room ids available")
	}

	for _, roomID := range roomIDs {
		t.Run(fmt.Sprintf("room-%d", roomID), func(t *testing.T) {
			urls, err := simulateGetStreamURLsV2(t, client, roomID, WithOnlyAudio(true))
			if err != nil {
				switch {
				case errors.Is(err, ErrRoomNotFound):
					t.Skip("room not found")
				case errors.Is(err, ErrStreamGeoRestricted):
					t.Skip("room geo restricted")
				default:
					t.Fatalf("failed to fetch audio-only urls for room %d: %v", roomID, err)
				}
			}

			if len(urls) == 0 {
				t.Skip("no audio-only urls available")
			}

			for _, stream := range urls {
				if !isAudioOnlyStreamURL(stream.URL) {
					t.Fatalf("audio-only stream URL missing ptype=1: %s", stream.URL)
				}
			}
		})
	}
}

func TestGetStreamUrlsV2NonAudioHasNoPtype1(t *testing.T) {
	var client *Client
	app := fxtest.New(t,
		config.Module,
		Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)

	app.RequireStart()
	defer app.RequireStop()

	roomIDs := testutil.LiveRoomIDs(t, 10)
	if len(roomIDs) == 0 {
		t.Skip("no live room ids available")
	}

	for _, roomID := range roomIDs {
		t.Run(fmt.Sprintf("room-%d", roomID), func(t *testing.T) {
			urls, err := simulateGetStreamURLsV2(t, client, roomID)
			if err != nil {
				switch {
				case errors.Is(err, ErrRoomNotFound):
					t.Skip("room not found")
				case errors.Is(err, ErrStreamGeoRestricted):
					t.Skip("room geo restricted")
				default:
					t.Fatalf("failed to fetch non-audio urls for room %d: %v", roomID, err)
				}
			}

			if len(urls) == 0 {
				t.Skip("no urls available")
			}

			for _, stream := range urls {
				if isAudioOnlyStreamURL(stream.URL) {
					t.Fatalf("non-audio request returned ptype=1 url: %s", stream.URL)
				}
			}
		})
	}
}

func TestGetStreamUrlsV2OnlyAudioExperimental(t *testing.T) {
	var client *Client
	app := fxtest.New(t,
		config.Module,
		Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)

	app.RequireStart()
	defer app.RequireStop()

	roomID := testutil.LiveRoomID(t)

	normalResults, err := fetchStreamQueryParams(t, client, roomID)
	if err != nil {
		t.Fatal(err)
	}
	audioResults, err := fetchStreamQueryParams(t, client, roomID, WithOnlyAudio(true))
	if err != nil {
		t.Fatal(err)
	}

	t.Log("--- normal stream query params ---")
	logParams(t, normalResults)
	t.Log("--- only_audio=1 stream query params ---")
	logParams(t, audioResults)
	t.Log("--- diff between normal and only_audio=1 ---")
	logParamDiff(t, normalResults, audioResults)
}

func fetchStreamQueryParams(t *testing.T, client *Client, roomID int, opts ...GetStreamURLsOption) (map[string][]string, error) {
	urls, err := simulateGetStreamURLsV2(t, client, roomID, opts...)
	if err != nil {
		return nil, err
	}

	if len(urls) == 0 {
		return nil, nil
	}

	allParams := make(map[string][]string)
	for _, stream := range urls {
		parsed, err := url.Parse(stream.URL)
		if err != nil {
			t.Logf("invalid url format url=%s err=%v", stream.URL, err)
			continue
		}
		params, err := url.ParseQuery(parsed.RawQuery)
		if err != nil {
			t.Logf("invalid query url=%s err=%v", stream.URL, err)
			continue
		}
		for k, v := range params {
			allParams[k] = uniqueAppend(allParams[k], v)
		}
	}
	return allParams, nil
}

func uniqueAppend(slice []string, values []string) []string {
	existing := make(map[string]struct{}, len(slice))
	for _, v := range slice {
		existing[v] = struct{}{}
	}
	for _, v := range values {
		if _, ok := existing[v]; !ok {
			slice = append(slice, v)
			existing[v] = struct{}{}
		}
	}
	return slice
}

func logParams(t *testing.T, params map[string][]string) {
	if params == nil {
		t.Log("no playurl params returned")
		return
	}
	pretty, err := json.MarshalIndent(params, "", "  ")
	if err != nil {
		t.Logf("failed to marshal params: %v", err)
		return
	}
	t.Logf("%s", string(pretty))
}

func logParamDiff(t *testing.T, normal, audio map[string][]string) {
	if normal == nil && audio == nil {
		t.Log("no params returned for either request")
		return
	}
	if normal == nil {
		normal = map[string][]string{}
	}
	if audio == nil {
		audio = map[string][]string{}
	}

	keys := make([]string, 0, len(normal)+len(audio))
	seen := make(map[string]struct{}, len(normal)+len(audio))
	for k := range normal {
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	for k := range audio {
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	diff := make(map[string]map[string][]string)
	for _, key := range keys {
		nv, nok := normal[key]
		av, aok := audio[key]
		if !nok {
			diff[key] = map[string][]string{"only_audio": av}
			continue
		}
		if !aok {
			diff[key] = map[string][]string{"normal": nv}
			continue
		}
		if !equalStringSlice(nv, av) {
			diff[key] = map[string][]string{"normal": nv, "only_audio": av}
		}
	}

	if len(diff) == 0 {
		t.Log("no differences in query params")
		return
	}

	pretty, err := json.MarshalIndent(diff, "", "  ")
	if err != nil {
		t.Logf("failed to marshal diff: %v", err)
		return
	}
	t.Logf("%s", string(pretty))
}

func simulateGetStreamURLsV2(t *testing.T, client *Client, roomID int, opts ...GetStreamURLsOption) ([]StreamURLInfo, error) {
	req := client.liveClient.R()
	options := defaultGetStreamURLsOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	for _, profile := range options.profiles {
		if !profile.IsValid() {
			return nil, ErrInvalidStreamProfile
		}
	}

	for _, codec := range options.codecs {
		if !codec.IsValid() {
			return nil, ErrInvalidStreamCodec
		}
	}

	if !options.qn.IsValid() {
		return nil, ErrInvalidStreamQuality
	}

	protocolRaw, formatRaw := profileQueryParams(options.profiles)
	protocolStr := utils.EmptyOrElse(protocolRaw, "0,1")
	formatStr := utils.EmptyOrElse(formatRaw, "0,1,2")
	codecStr := utils.EmptyOrElse(joinCodecs(options.codecs), "0,1,2")

	params := map[string]string{
		"room_id":      fmt.Sprint(roomID),
		"qn":           fmt.Sprint(int(options.qn)),
		"no_playurl":   "0",
		"mask":         "1",
		"platform":     "web",
		"protocol":     protocolStr,
		"format":       formatStr,
		"codec":        codecStr,
		"dolby":        "5",
		"panorama":     "1",
		"hdr_type":     "0,1",
		"web_location": "444.8",
		"only_audio":   utils.Ternary(options.onlyAudio, "1", "0"),
	}

	req.SetQueryParams(params)
	newQueryParam, err := client.wbi.SignQuery(req.QueryParam, time.Now())
	if err != nil {
		return nil, fmt.Errorf("无法签名 wbi：%v", err)
	}
	req.QueryParam = newQueryParam

	resp, err := req.Get(v2StreamAPI)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("状态码：%d", resp.StatusCode())
	}

	var sr StreamResponseV2
	if err := json.Unmarshal(resp.Body(), &sr); err != nil {
		return nil, err
	} else if sr.Code != 0 {
		return nil, mapStreamAPIError(sr.Code, sr.Message)
	}

	if sr.Data.PlayurlInfo == nil || sr.Data.PlayurlInfo.Playurl == nil {
		return []StreamURLInfo{}, nil
	}

	streamInfos := make([]StreamURLInfo, 0)
	for _, stream := range sr.Data.PlayurlInfo.Playurl.Streams {
		for _, format := range stream.Formats {
			if !containFormat(options.profiles, format.FormatName) {
				continue
			}
			for _, codec := range format.Codecs {
				for _, urlInfo := range codec.UrlInfos {
					fullURL := urlInfo.Host + codec.BaseUrl + urlInfo.Extra
					audioOnlyStream := isAudioOnlyStreamURL(fullURL)
					streamInfos = append(streamInfos, StreamURLInfo{
						Protocol:    stream.ProtocolName,
						Format:      format.FormatName,
						Codec:       codec.CodecName,
						URL:         fullURL,
						Qn:          codec.CurrentQn,
						AcceptQn:    codec.AcceptQn,
						IsAudioOnly: audioOnlyStream,
					})
				}
			}
		}
	}

	return streamInfos, nil
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aCopy := append([]string(nil), a...)
	bCopy := append([]string(nil), b...)
	sort.Strings(aCopy)
	sort.Strings(bCopy)
	for i := range aCopy {
		if aCopy[i] != bCopy[i] {
			return false
		}
	}
	return true
}

func init() {
	if os.Getenv("CI") != "" {
		os.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
	}
}
