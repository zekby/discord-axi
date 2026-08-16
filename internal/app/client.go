// Package app implements the discord-axi commands on top of the same Discord
// stack Discordo uses: arikawa for the API, ningen for read state, and
// Discordo's own HTTP/TLS transport.
package app

import (
	"context"
	"errors"
	"net/http"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
	"github.com/zekby/discord-axi/internal/axi"
	dhttp "github.com/zekby/discord-axi/internal/discordo/http"
)

// AccountFlag selects a stored account. It is a global: every command accepts
// it, and it is never reported as an unknown flag.
const AccountFlag = "--account"

func AccountGlobal() axi.Flag {
	return axi.Flag{
		Name:  AccountFlag,
		Value: "name",
		Desc:  "Run as this stored account instead of the default",
	}
}

// readOnlyDriver refuses anything that is not a read. The check sits at the
// transport rather than in each command, so a command added later cannot
// forget it and no request can slip past.
type readOnlyDriver struct {
	httpdriver.Client
	profile string
}

func (d readOnlyDriver) NewRequest(ctx context.Context, method, url string) (httpdriver.Request, error) {
	if method != http.MethodGet {
		return nil, axi.Fail("READ_ONLY",
			`account "`+d.profile+`" is read-only, so this cannot `+method,
			widenScopeHint(d.profile),
			"Reading is what carries no reported risk of the account being disabled")
	}
	return d.Client.NewRequest(ctx, method, url)
}

// NewClient builds the API client with Discordo's browser-shaped transport and
// headers, the pacer that keeps an agent from firing requests in bursts, and
// the account's scope.
func NewClient(credentials Credentials) *api.Client {
	RefreshClientBuildNumber()

	var driver httpdriver.Client = dhttp.Driver()
	if credentials.ReadOnly() {
		driver = readOnlyDriver{Client: driver, profile: credentials.Profile}
	}

	client := dhttp.NewClientWithDriver(credentials.Authorization(), driver)
	client.OnRequest = append(client.OnRequest, httputil.WithHeaders(dhttp.Headers()), pacedRequest)
	return client
}

// Client resolves the account for this invocation and builds its client.
func Client(inv *axi.Invocation) (*api.Client, Credentials, error) {
	credentials, err := Resolve(inv)
	if err != nil {
		return nil, Credentials{}, err
	}
	return NewClient(credentials), credentials, nil
}

// RequireWrite refuses a mutating command up front, so the agent gets the same
// answer whether or not the command would have reached the network.
func RequireWrite(credentials Credentials) error {
	if !credentials.ReadOnly() {
		return nil
	}
	return axi.Fail("READ_ONLY", `account "`+credentials.Profile+`" is read-only`,
		widenScopeHint(credentials.Profile),
		"A read-only account cannot be the one that gets disabled for automated writing")
}

// widenScopeHint names the way to allow writes for the way these credentials
// arrived: a stored account is changed with `auth scope`, a token in the
// environment only with the environment.
func widenScopeHint(profile string) string {
	if profile == envProfile {
		return "Set " + ScopeEnvVar + "=" + ScopeWrite + " to allow writes with the token in " + TokenEnvVar
	}
	return "Run `" + axi.Binary() + " auth scope " + profile + " --write` to allow writes"
}

// Translate turns a Discord API failure into an actionable AXI error. Raw
// dependency output never reaches stdout.
func Translate(err error) error {
	if err == nil {
		return nil
	}
	var axiErr *axi.Error
	if errors.As(err, &axiErr) {
		return err
	}

	var httpErr *httputil.HTTPError
	if !errors.As(err, &httpErr) {
		return axi.Fail("NETWORK_ERROR", "could not reach Discord",
			"Check the network connection and run the command again")
	}

	switch httpErr.Status {
	case 401:
		return axi.Fail("NOT_AUTHENTICATED", "token rejected by Discord",
			"Run `"+axi.Binary()+` login --token "<token>"`+"` with a current token")
	case 403:
		return axi.Fail("FORBIDDEN", "access denied: "+message(httpErr),
			"This account lacks permission for that channel or guild",
			"Run `"+axi.Binary()+" guilds` to see what it can reach")
	case 404:
		return axi.Fail("NOT_FOUND", "not found: "+message(httpErr),
			"Run `"+axi.Binary()+" guilds` to list reachable guilds",
			"Run `"+axi.Binary()+` channels "<guild>"`+"` to list channel ids")
	case 429:
		return axi.Fail("RATE_LIMITED", "rate limited by Discord",
			"Wait a few seconds and run the command again")
	default:
		return axi.Fail("API_ERROR", "Discord rejected the request: "+message(httpErr))
	}
}

func message(err *httputil.HTTPError) string {
	if err.Message != "" {
		return err.Message
	}
	return statusText(err.Status)
}

func statusText(status int) string {
	switch status {
	case 400:
		return "invalid request"
	case 500, 502, 503:
		return "Discord is unavailable"
	default:
		return "unexpected status"
	}
}

// NotFound reports whether an error is a Discord 404, used to make deletions
// idempotent.
func NotFound(err error) bool {
	var httpErr *httputil.HTTPError
	return errors.As(err, &httpErr) && httpErr.Status == 404
}

func isUnauthorized(err error) bool {
	var axiErr *axi.Error
	return errors.As(Translate(err), &axiErr) && axiErr.Code == "NOT_AUTHENTICATED"
}
