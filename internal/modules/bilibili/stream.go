package bilibili

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	"github.com/bilirec/bilirec/pkg/fp"
	"github.com/bilirec/bilirec/utils"
)

type (
	StreamResponseV1 struct {
		Code    int        `json:"code"`
		Message string     `json:"message"`
		TTL     int        `json:"ttl"`
		Data    StreamData `json:"data"`
	}

	StreamData struct {
		CurrentQuality     int           `json:"current_quality"`
		AcceptQuality      []string      `json:"accept_quality"`
		CurrentQn          int           `json:"current_qn"`
		QualityDescription []QualityDesc `json:"quality_description"`
		Durl               []StreamURL   `json:"durl"`
	}

	QualityDesc struct {
		Qn   int    `json:"qn"`
		Desc string `json:"desc"`
	}

	StreamURL struct {
		URL        string `json:"url"`
		Length     int    `json:"length"`
		Order      int    `json:"order"`
		StreamType int    `json:"stream_type"`
		P2PType    int    `json:"p2p_type"`
	}
)

type (
	StreamResponseV2 struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		TTL     int    `json:"ttl"`
		Data    struct {
			PlayurlInfo *PlayurlInfo `json:"playurl_info"`
		} `json:"data"`
	}

	RoomPlayInfo struct {
		LiveStatus  int          `json:"live_status"`
		Encrypted   bool         `json:"encrypted"`
		PlayurlInfo *PlayurlInfo `json:"playurl_info"`
	}

	PlayurlInfo struct {
		Playurl *Playurl `json:"playurl"`
	}

	Playurl struct {
		Streams []StreamItem `json:"stream"`
	}

	StreamItem struct {
		ProtocolName string       `json:"protocol_name"`
		Formats      []FormatItem `json:"format"`
	}

	FormatItem struct {
		FormatName string      `json:"format_name"`
		Codecs     []CodecItem `json:"codec"`
	}

	CodecItem struct {
		CodecName string        `json:"codec_name"`
		BaseUrl   string        `json:"base_url"`
		CurrentQn int           `json:"current_qn"`
		AcceptQn  []int         `json:"accept_qn"`
		UrlInfos  []UrlInfoItem `json:"url_info"`
	}

	UrlInfoItem struct {
		Host  string `json:"host"`
		Extra string `json:"extra"`
	}

	StreamURLInfo struct {
		Protocol    string
		Format      string
		Codec       string
		URL         string
		Qn          int
		AcceptQn    []int
		IsAudioOnly bool
	}
)

var ErrInvalidStreamProfile = errors.New("无效的流配置")
var ErrInvalidStreamCodec = errors.New("无效的流编码")
var ErrInvalidStreamQuality = errors.New("无效的流画质")

const v1StreamAPI = "https://api.live.bilibili.com/room/v1/Room/playUrl"
const v2StreamAPI = "https://api.live.bilibili.com/xlive/web-room/v2/index/getRoomPlayInfo"

const (
	streamAPICodeRoomNotFound    = 19002003
	streamAPICodeGeoRestricted   = 60005
)

func mapStreamAPIError(code int, message string) error {
	switch code {
	case streamAPICodeRoomNotFound:
		return ErrRoomNotFound
	case streamAPICodeGeoRestricted:
		return ErrStreamGeoRestricted
	default:
		return fmt.Errorf("获取流 URL 失败：%s（代码 %d）", message, code)
	}
}

func (c *Client) GetStreamURLs(roomID int) ([]string, error) {
	client := c.liveClient.R()
	client.SetQueryParams(map[string]string{
		"cid":      fmt.Sprint(roomID),
		"qn":       "10000",
		"platform": "web",
	})

	resp, err := client.Get(v1StreamAPI)
	if err != nil {
		return nil, err
	} else if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("状态码：%d", resp.StatusCode())
	}

	var sr StreamResponseV1
	if err := json.Unmarshal(resp.Body(), &sr); err != nil {
		return nil, err
	} else if sr.Code != 0 {
		return nil, mapStreamAPIError(sr.Code, sr.Message)
	}

	return fp.Map(sr.Data.Durl, func(durl StreamURL) string {
		return durl.URL
	}), nil
}

func (c *Client) GetStreamURLsV2(roomID int, opts ...GetStreamURLsOption) ([]StreamURLInfo, error) {
	client := c.liveClient.R()
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
		"codec":        codecStr, // 0: avc, 1: hevc, 2: unknown
		"dolby":        "5",
		"panorama":     "1",
		"hdr_type":     "0,1",
		"web_location": "444.8",
		"only_audio":   utils.Ternary(options.onlyAudio, "1", "0"), // 希望可以防止錯誤拿到音頻流
	}

	client.SetQueryParams(params)
	newQueryParam, err := c.wbi.SignQuery(client.QueryParam, time.Now())
	if err != nil {
		return nil, fmt.Errorf("无法签名 wbi：%v", err)
	}
	client.QueryParam = newQueryParam

	resp, err := client.Get(v2StreamAPI)
	if err != nil {
		return nil, err
	} else if resp.StatusCode() != 200 {
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
					// 雖然加了 only_audio=1/0 理應不會有非預期的流，但是再做一次檢測
					if options.onlyAudio != audioOnlyStream {
						continue
					}
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

	sortStreams(streamInfos, int(options.qn))
	return streamInfos, nil
}
func containFormat(profiles []StreamProfile, format string) bool {
	return slices.ContainsFunc(profiles, func(profile StreamProfile) bool {
		switch format {
		case "flv":
			return profile == ProfileHTTPFLV
		case "ts":
			return profile == ProfileHLSTS
		case "fmp4":
			return profile == ProfileHLSFMP4
		default:
			return false
		}
	})
}

func isAudioOnlyStreamURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Query().Get("ptype") == "1"
}
