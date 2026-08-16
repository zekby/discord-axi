package app

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	dhttp "github.com/zekby/discord-axi/internal/discordo/http"
)

// The identify payload carries the web client's build number. A number frozen at
// compile time drifts away from what real browsers report within weeks, so it is
// refreshed from Discord's own login page and cached.
const (
	buildNumberTTL     = 24 * time.Hour
	buildNumberTimeout = 5 * time.Second
)

var buildNumberPattern = regexp.MustCompile(`"BUILD_NUMBER":\s*"(\d+)"`)

type buildNumberCache struct {
	Value   int   `json:"value"`
	Fetched int64 `json:"fetched"`
}

var refreshOnce sync.Once

// RefreshClientBuildNumber updates the reported build number at most once a day.
// Any failure leaves the compiled-in value in place: a slightly stale number is
// better than no request at all.
func RefreshClientBuildNumber() {
	refreshOnce.Do(func() {
		path := filepath.Join(StateDir(), "build-number.json")

		if cached, err := readBuildNumber(path); err == nil {
			if time.Since(time.Unix(cached.Fetched, 0)) < buildNumberTTL {
				dhttp.ClientBuildNumber = cached.Value
				return
			}
		}

		fetched, err := fetchBuildNumber()
		if err != nil {
			return
		}
		dhttp.ClientBuildNumber = fetched
		if encoded, err := json.Marshal(buildNumberCache{Value: fetched, Fetched: time.Now().Unix()}); err == nil {
			_ = os.WriteFile(path, encoded, 0o600)
		}
	})
}

func readBuildNumber(path string) (buildNumberCache, error) {
	var cached buildNumberCache
	raw, err := os.ReadFile(path)
	if err != nil {
		return cached, err
	}
	if err := json.Unmarshal(raw, &cached); err != nil || cached.Value == 0 {
		return cached, os.ErrInvalid
	}
	return cached, nil
}

func fetchBuildNumber() (int, error) {
	request, err := http.NewRequest(http.MethodGet, api.BaseEndpoint+"/login", nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("User-Agent", dhttp.BrowserUserAgent())

	client := http.Client{Transport: dhttp.NewTransport(), Timeout: buildNumberTimeout}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, os.ErrInvalid
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return 0, err
	}
	matches := buildNumberPattern.FindSubmatch(body)
	if len(matches) < 2 {
		return 0, os.ErrNotExist
	}
	return strconv.Atoi(string(matches[1]))
}
