//go:build no_spoof_tls_fingerprint

package http

import (
	"net/http"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
)

func NewClient(token string) *api.Client {
	return NewClientWithDriver(token, Driver())
}

// Driver exposes the HTTP driver so a caller can wrap it, for example to refuse
// writes on a read-only account. Added for discord-axi.
func Driver() httpdriver.Client {
	return httpdriver.WrapClient(http.Client{Transport: NewTransport()})
}

// NewClientWithDriver builds the API client on a caller-supplied driver.
// Added for discord-axi.
func NewClientWithDriver(token string, driver httpdriver.Client) *api.Client {
	client := api.NewCustomClient(token, httputil.NewClientWithDriver(driver))
	client.UserAgent = BrowserUserAgent()
	return client
}
