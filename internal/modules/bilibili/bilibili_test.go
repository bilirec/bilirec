package bilibili_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/testutil"
	"github.com/go-resty/resty/v2"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

func TestGetStreamURLsNotExistRoom(t *testing.T) {
	var client *bilibili.Client
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)

	app.RequireStart()
	defer app.RequireStop()

	_, err := client.GetStreamURLs(9999999999)
	if err != nil {
		if bilibili.IsErrRoomNotFound(err) {
			t.Log("room not found as expected")
			return
		}
		t.Fatal(err)
	}
	t.Fatal("expected room not found error, but got none")

	_, err = client.GetStreamURLsV2(9999999999)
	if err != nil {
		if bilibili.IsErrRoomNotFound(err) {
			t.Log("room not found as expected")
			return
		}
		t.Fatal(err)
	}
	t.Fatal("expected room not found error, but got none")
}

func TestGetRoomInfoNotExistRoom(t *testing.T) {
	var client *bilibili.Client
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)

	app.RequireStart()
	defer app.RequireStop()

	_, err := client.GetLiveRoomInfo(9999999999)
	if err != nil {
		if bilibili.IsErrRoomNotFound(err) {
			t.Log("room not found as expected")
			return
		}
		t.Fatal(err)
	}
	t.Fatal("expected room not found error, but got none")
}

func TestGetLiveRoomInfo(t *testing.T) {
	var client *bilibili.Client
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)
	app.RequireStart()
	defer app.RequireStop()
	info, err := client.GetLiveRoomInfo(8222458)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Room Info: %+v", info)
}

func TestGetStreamUrls(t *testing.T) {
	var client *bilibili.Client
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)

	app.RequireStart()
	defer app.RequireStop()

	urls, err := client.GetStreamURLs(8222458)
	if err != nil {
		if bilibili.IsErrRoomNotFound(err) {
			t.Skip("room not found, skipped")
		}
		t.Fatal(err)
	}
	for _, url := range urls {
		t.Logf("Stream URL: %s", url)
	}
}

func TestGetStreamUrlsV2(t *testing.T) {
	var client *bilibili.Client
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)
	app.RequireStart()
	defer app.RequireStop()

	urls, err := client.GetStreamURLsV2(22908869)
	if err != nil {
		if bilibili.IsErrRoomNotFound(err) {
			t.Skip("room not found, skipped")
		}
		t.Fatal(err)
	}
	for _, streamInfo := range urls {
		t.Logf("Stream URL: %s, protocol=%s, format=%s, codec=%s, qn=%d", streamInfo.URL, streamInfo.Protocol, streamInfo.Format, streamInfo.Codec, streamInfo.Qn)
	}
}

func TestGetStreamUrlsV2WithProfiles(t *testing.T) {
	var client *bilibili.Client
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)
	app.RequireStart()
	defer app.RequireStop()

	roomID := testutil.LiveRoomID(t)

	t.Run("http-flv profile", func(t *testing.T) {
		urls, err := client.GetStreamURLsV2(roomID,
			bilibili.WithProfiles(bilibili.ProfileHTTPFLV),
		)
		if err != nil {
			if bilibili.IsErrRoomNotFound(err) {
				t.Skip("room not found, skipped")
			}
			t.Fatal(err)
		}
		if len(urls) == 0 {
			t.Skip("no flv stream urls available currently")
		}
		for _, streamInfo := range urls {
			lower := strings.ToLower(streamInfo.URL)
			if strings.Contains(lower, ".m3u8") {
				t.Fatalf("unexpected hls url in flv profile result: %s", streamInfo.URL)
			}
			if !strings.Contains(lower, ".flv") {
				t.Fatalf("expected .flv url in flv profile result: %s", streamInfo.URL)
			}
			t.Logf("expected http-flv stream url: %s (protocol=%s, format=%s, codec=%s, qn=%d)", streamInfo.URL, streamInfo.Protocol, streamInfo.Format, streamInfo.Codec, streamInfo.Qn)
		}
	})

	t.Run("hls-ts profile", func(t *testing.T) {
		urls, err := client.GetStreamURLsV2(roomID,
			bilibili.WithProfiles(bilibili.ProfileHLSTS),
		)
		if err != nil {
			if bilibili.IsErrRoomNotFound(err) {
				t.Skip("room not found, skipped")
			}
			t.Fatal(err)
		}
		if len(urls) == 0 {
			t.Skip("no hls-ts stream urls available currently")
		}
		for _, streamInfo := range urls {
			lower := strings.ToLower(streamInfo.URL)
			if strings.Contains(lower, ".flv") {
				t.Fatalf("unexpected flv url in hls-ts profile result: %s", streamInfo.URL)
			}
			if !strings.Contains(lower, ".m3u8") {
				t.Fatalf("expected .m3u8 url in hls-ts profile result: %s", streamInfo.URL)
			}
			t.Logf("expected hls-ts stream url: %s (protocol=%s, format=%s, codec=%s, qn=%d)", streamInfo.URL, streamInfo.Protocol, streamInfo.Format, streamInfo.Codec, streamInfo.Qn)
		}
	})

	t.Run("hls-fmp4 profile", func(t *testing.T) {
		urls, err := client.GetStreamURLsV2(roomID,
			bilibili.WithProfiles(bilibili.ProfileHLSFMP4),
		)
		if err != nil {
			if bilibili.IsErrRoomNotFound(err) {
				t.Skip("room not found, skipped")
			}
			t.Fatal(err)
		}
		if len(urls) == 0 {
			t.Skip("no hls-ts stream urls available currently")
		}
		for _, streamInfo := range urls {
			lower := strings.ToLower(streamInfo.URL)
			if strings.Contains(lower, ".flv") {
				t.Fatalf("unexpected flv url in hls-ts profile result: %s", streamInfo.URL)
			}
			if !strings.Contains(lower, ".m3u8") {
				t.Fatalf("expected .m3u8 url in hls-ts profile result: %s", streamInfo.URL)
			}
			t.Logf("expected hls-ts stream url: %s (protocol=%s, format=%s, codec=%s, qn=%d)", streamInfo.URL, streamInfo.Protocol, streamInfo.Format, streamInfo.Codec, streamInfo.Qn)
		}
	})
}

func TestHeaders(t *testing.T) {
	var client *bilibili.Client
	app := fxtest.New(t,
		config.Module,
		bilibili.Module,
		fx.Populate(&client),
		fx.StartTimeout(25*time.Second),
	)
	app.RequireStart()
	defer app.RequireStop()

	a := &strings.Builder{}
	if _, err := client.Do(func(req *resty.Request) (*resty.Response, error) {
		err := req.Header.Write(a)
		return nil, err
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("client header: ", a)

	b := &strings.Builder{}
	if _, err := client.DoLive(func(req *resty.Request) (*resty.Response, error) {
		err := req.Header.Write(b)
		return nil, err
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("live client header: ", b)

}

func init() {
	// if os.Getenv("CI") != "" {
	os.Setenv("BILIBILI_LOGIN_MODE", "anonymous")
	// }
}
