// Package app implements the discord-axi commands on top of the same Discord
// stack Discordo uses: arikawa for the API, ningen for read state, and
// Discordo's own HTTP/TLS transport.
package app

import (
	"errors"
	"os"
	"strings"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/zekby/discord-axi/internal/axi"
	dhttp "github.com/zekby/discord-axi/internal/discordo/http"
	"github.com/zekby/discord-axi/internal/discordo/keyring"
)

// TokenEnvVar overrides the stored token, mirroring Discordo's DISCORDO_TOKEN.
const TokenEnvVar = "DISCORD_AXI_TOKEN"

// BotPrefix is what Discord expects in front of a bot token; user tokens are
// sent bare.
const BotPrefix = "Bot "

func Token() (string, error) {
	if token := os.Getenv(TokenEnvVar); token != "" {
		return token, nil
	}
	token, err := keyring.GetToken()
	if err != nil || token == "" {
		return "", axi.Fail("NOT_AUTHENTICATED", "not logged in",
			"Run `"+axi.Binary()+` login --token "<token>"`+"` to authenticate",
			"Or set "+TokenEnvVar+" in the environment")
	}
	return token, nil
}

// NewClient builds the API client with Discordo's browser-shaped transport and
// headers, so requests look identical to what Discordo sends, plus the pacer
// that keeps an agent from firing them in bursts.
func NewClient(token string) *api.Client {
	RefreshClientBuildNumber()
	client := dhttp.NewClient(token)
	client.OnRequest = append(client.OnRequest, httputil.WithHeaders(dhttp.Headers()), pacedRequest)
	return client
}

func Client() (*api.Client, error) {
	token, err := Token()
	if err != nil {
		return nil, err
	}
	return NewClient(token), nil
}

func IsBot(token string) bool { return strings.HasPrefix(token, BotPrefix) }

func TokenKind(token string) string {
	if IsBot(token) {
		return "bot"
	}
	return "user"
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
