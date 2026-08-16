package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
)

func TestFetchBuildNumberReadsTheLoginPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("User-Agent") == "" {
			t.Error("the build number request must carry the browser user agent")
		}
		_, _ = w.Write([]byte(`<script>window.GLOBAL_ENV={"BUILD_NUMBER": "594031","API_ENDPOINT":"//discord.com/api"}</script>`))
	}))
	defer server.Close()

	saved := api.BaseEndpoint
	api.BaseEndpoint = server.URL
	defer func() { api.BaseEndpoint = saved }()

	got, err := fetchBuildNumber()
	if err != nil {
		t.Fatal(err)
	}
	if got != 594031 {
		t.Fatalf("build number = %d, want 594031", got)
	}
}

func TestFetchBuildNumberFailsLoudlyOnAnUnexpectedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>no globals here</html>"))
	}))
	defer server.Close()

	saved := api.BaseEndpoint
	api.BaseEndpoint = server.URL
	defer func() { api.BaseEndpoint = saved }()

	if _, err := fetchBuildNumber(); err == nil {
		t.Fatal("expected an error when the build number is absent")
	}
}

func TestCachedBuildNumberIsReusedUntilItExpires(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "build-number.json")

	fresh, _ := json.Marshal(buildNumberCache{Value: 594031, Fetched: time.Now().Unix()})
	if err := os.WriteFile(path, fresh, 0o600); err != nil {
		t.Fatal(err)
	}
	cached, err := readBuildNumber(path)
	if err != nil || cached.Value != 594031 {
		t.Fatalf("cached = %+v, err = %v", cached, err)
	}
	if time.Since(time.Unix(cached.Fetched, 0)) >= buildNumberTTL {
		t.Fatal("a just-written cache entry must count as fresh")
	}

	stale, _ := json.Marshal(buildNumberCache{Value: 1, Fetched: time.Now().Add(-2 * buildNumberTTL).Unix()})
	if err := os.WriteFile(path, stale, 0o600); err != nil {
		t.Fatal(err)
	}
	expired, err := readBuildNumber(path)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(time.Unix(expired.Fetched, 0)) < buildNumberTTL {
		t.Fatal("an entry older than the TTL must count as expired")
	}
}

func TestCorruptBuildNumberCacheIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build-number.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBuildNumber(path); err == nil {
		t.Fatal("a corrupt cache must be treated as missing, not trusted")
	}
}
