package updatecheck

import (
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/bilirec/bilirec/pkg/stdoutbox"
	"github.com/sirupsen/logrus"
	"github.com/tcnksm/go-latest"
)

const (
	githubOwner      = "bilirec"
	githubRepository = "bilirec"
	releasesURL      = "https://github.com/bilirec/bilirec/releases/latest"
)

// currentVersionInjected is set at build time for production releases.
var currentVersionInjected = ""

var cacheMu sync.RWMutex
var cached Result

var logger = logrus.WithField("module", "updatecheck")

// Result holds version check state for logging and REST responses.
type Result struct {
	Current        string `json:"current"`
	Latest         string `json:"latest"`
	Outdated       bool   `json:"outdated"`
	Checked        bool   `json:"checked"`
	URL            string `json:"url"`
	Error          string `json:"error"`
	ErrorCode      string `json:"error_code,omitempty"`
	RetryAfterSecs int    `json:"retry_after_secs,omitempty"`
}

// Current returns the build-time injected version, or empty for non-production builds.
func Current() string {
	return currentVersionInjected
}

// Cached returns the last check result. Before any Check(), only Current is set.
func Cached() Result {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return snapshotLocked()
}

// Check queries GitHub for the latest release tag and compares with the embedded version.
// It ignores CHECK_UPDATE and updates the package-level cache.
func Check() (Result, error) {
	current := Current()
	if current == "" {
		res := Result{Current: "", URL: releasesURL}
		setCache(res)
		return res, nil
	}

	target := strings.TrimPrefix(strings.TrimSpace(current), "v")
	githubTag := &latest.GithubTag{
		Owner:             githubOwner,
		Repository:        githubRepository,
		FixVersionStrFunc: latest.DeleteFrontV(),
	}

	checkRes, err := latest.Check(githubTag, target)
	if err != nil {
		res := failureResult(current, err)
		setCache(res)
		return res, err
	}

	logger.Debugf("latest version: %s, current version: %s, outdated: %v", checkRes.Current, current, checkRes.Outdated)

	latestTag := checkRes.Current
	if latestTag != "" && !strings.HasPrefix(latestTag, "v") {
		latestTag = "v" + latestTag
	}

	res := Result{
		Current:  current,
		Latest:   latestTag,
		Outdated: checkRes.Outdated,
		Checked:  true,
		URL:      releasesURL,
	}
	setCache(res)
	return res, nil
}

// InvokeCheck runs a one-time update check on startup when enabled.
func InvokeCheck() {
	if !shouldCheck() {
		return
	}

	res, err := Check()
	if err != nil {
		LogFailure(res, err)
		return
	}
	if res.Outdated {
		printUpdateBox(res)
	}
}

func shouldCheck() bool {
	if currentVersionInjected == "" {
		return false
	}
	return checkUpdateEnabled()
}

func checkUpdateEnabled() bool {
	v, ok := os.LookupEnv("CHECK_UPDATE")
	if !ok {
		return runtime.GOOS != "android"
	}
	return v == "true"
}

func printUpdateBox(res Result) {
	stdoutbox.Print(
		"新版本可用!",
		"当前版本:  "+res.Current,
		"最新版本:  "+res.Latest,
		"下载地址:  "+res.URL,
	)
}

func setCache(res Result) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	cached = res
}

func snapshotLocked() Result {
	if cached.Checked || cached.Error != "" {
		return cached
	}
	return Result{
		Current: Current(),
		URL:     releasesURL,
	}
}
