package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ayn2op/arikawa/v3/api"
)

const (
	guildID     = "100000000000000001"
	stagingID   = "100000000000000002"
	generalID   = "200000000000000001"
	voiceID     = "200000000000000002"
	longMessage = 1500
)

// mockDiscord points arikawa's endpoints at a local server for the duration of
// one test. The endpoints are package variables, so each one is restored.
func mockDiscord(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)

	saved := []struct {
		target *string
		value  string
	}{
		{&api.BaseEndpoint, api.BaseEndpoint},
		{&api.Endpoint, api.Endpoint},
		{&api.EndpointUsers, api.EndpointUsers},
		{&api.EndpointMe, api.EndpointMe},
		{&api.EndpointGuilds, api.EndpointGuilds},
		{&api.EndpointChannels, api.EndpointChannels},
	}
	api.BaseEndpoint = server.URL
	api.Endpoint = server.URL + api.Path + "/"
	api.EndpointUsers = api.Endpoint + "users/"
	api.EndpointMe = api.EndpointUsers + "@me"
	api.EndpointGuilds = api.Endpoint + "guilds/"
	api.EndpointChannels = api.Endpoint + "channels/"

	t.Cleanup(func() {
		for _, entry := range saved {
			*entry.target = entry.value
		}
		server.Close()
	})
	t.Setenv(TokenEnvVar, "test-user-token")
	// Keep the tests off the machine's real state and off the pacer's clock.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(MinIntervalEnvVar, "0")
}

func discordAPI(t *testing.T, requests *[]string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			*requests = append(*requests, r.Method+" "+r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		path := strings.TrimPrefix(r.URL.Path, api.Path)

		switch {
		case path == "/users/@me":
			write(w, map[string]any{"id": "1", "username": "axi", "global_name": "AXI Agent"})
		case path == "/users/@me/guilds":
			write(w, []map[string]any{
				{"id": guildID, "name": "Acme"},
				{"id": stagingID, "name": "Acme Staging"},
			})
		case path == "/users/@me/channels":
			write(w, []map[string]any{})
		case path == "/guilds/"+stagingID+"/channels":
			write(w, []map[string]any{})
		case path == "/guilds/"+guildID+"/channels":
			write(w, []map[string]any{
				{"id": generalID, "name": "general", "type": 0, "guild_id": guildID, "position": 1},
				{"id": voiceID, "name": "voice-room", "type": 2, "guild_id": guildID, "position": 2},
			})
		case path == "/channels/"+generalID+"/messages" && r.Method == http.MethodGet:
			write(w, []map[string]any{
				{
					"id":        "301",
					"author":    map[string]any{"username": "bob"},
					"timestamp": "2026-08-16T10:05:00.000000+00:00",
					"content":   strings.Repeat("x", longMessage),
				},
				{
					"id":        "300",
					"author":    map[string]any{"username": "alice"},
					"timestamp": "2026-08-16T10:00:00.000000+00:00",
					"content":   "hello",
				},
			})
		case path == "/channels/"+generalID+"/messages" && r.Method == http.MethodPost:
			write(w, map[string]any{"id": "999", "channel_id": generalID, "guild_id": guildID})
		case strings.HasSuffix(path, "/messages/404404") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			write(w, map[string]any{"code": 10008, "message": "Unknown Message"})
		case strings.HasSuffix(path, "/messages/500500") && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
			write(w, map[string]any{"message": "not mocked: " + path})
		}
	}
}

func write(w http.ResponseWriter, body any) {
	_ = json.NewEncoder(w).Encode(body)
}

func run(t *testing.T, argv ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	code := App("test").Run(argv, &out)
	return out.String(), code
}

func TestHomeShowsAccountAndGuilds(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t)
	if code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	for _, want := range []string{"bin: ", `account: "AXI Agent (env, user, read)"`, "guilds[2]{id,name}:", `"100000000000000001",Acme`} {
		if !strings.Contains(out, want) {
			t.Fatalf("home view is missing %q:\n%s", want, out)
		}
	}
}

func TestChannelsHidesVoiceUntilAsked(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t, "channels", "Acme")
	if code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "count: 1 of 1 total") || strings.Contains(out, "voice-room") {
		t.Fatalf("default listing should hide voice channels:\n%s", out)
	}

	all, _ := run(t, "channels", "Acme", "--all")
	if !strings.Contains(all, "voice-room") {
		t.Fatalf("--all should include voice channels:\n%s", all)
	}
}

func TestAmbiguousGuildNameListsCandidates(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t, "channels", "acme s")
	if code != 0 || !strings.Contains(out, "0 channels found in Acme Staging") {
		t.Fatalf("a unique partial match should resolve and state its zero:\n%s", out)
	}

	if out, code := run(t, "channels", "acme"); code != 0 || !strings.Contains(out, "guild: Acme") {
		t.Fatalf("an exact name must win over a substring match:\n%s", out)
	}

	out, code = run(t, "channels", "cme")
	if code != 2 {
		t.Fatalf("exit code = %d, want 2:\n%s", code, out)
	}
	if !strings.Contains(out, "matches 2 guilds") || !strings.Contains(out, "Acme Staging (id "+stagingID+")") {
		t.Fatalf("ambiguity error should list candidates:\n%s", out)
	}
}

func TestMessagesAreChronologicalAndTruncated(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t, "messages", "Acme/general")
	if code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if strings.Index(out, `"300"`) > strings.Index(out, `"301"`) {
		t.Fatalf("messages should print oldest first:\n%s", out)
	}
	if !strings.Contains(out, "truncated, 1500 chars total") || !strings.Contains(out, "--full") {
		t.Fatalf("long bodies should be truncated with an escape hatch:\n%s", out)
	}

	full, _ := run(t, "messages", "Acme/general", "--full")
	if strings.Contains(full, "truncated") {
		t.Fatalf("--full should print complete bodies:\n%s", full)
	}
}

func TestMessagesRejectsOversizedLimitBeforeCallingDiscord(t *testing.T) {
	var requests []string
	mockDiscord(t, discordAPI(t, &requests))

	out, code := run(t, "messages", "Acme/general", "--limit", "500")
	if code != 2 || !strings.Contains(out, "--limit must be 100 or less") {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if len(requests) != 0 {
		t.Fatalf("validation must happen before any request, got %v", requests)
	}
}

func TestSendReportsIDAndJumpURL(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	t.Setenv(ScopeEnvVar, ScopeWrite)

	out, code := run(t, "send", "Acme/general", "--content", "deploy finished")
	if code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if !strings.Contains(out, `id: "999"`) ||
		!strings.Contains(out, "https://discord.com/channels/"+guildID+"/"+generalID+"/999") {
		t.Fatalf("send should report the new message and its url:\n%s", out)
	}
}

func TestSendRequiresContentBeforeCallingDiscord(t *testing.T) {
	var requests []string
	mockDiscord(t, discordAPI(t, &requests))

	out, code := run(t, "send", "Acme/general")
	if code != 2 || !strings.Contains(out, "--content is required") {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if len(requests) != 0 {
		t.Fatalf("validation must happen before any request, got %v", requests)
	}
}

func TestDeletingAMissingMessageIsANoOp(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	t.Setenv(ScopeEnvVar, ScopeWrite)

	out, code := run(t, "delete", "Acme/general", "404404")
	if code != 0 {
		t.Fatalf("an already deleted message must exit 0, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "(no-op)") {
		t.Fatalf("expected a no-op acknowledgement:\n%s", out)
	}

	out, code = run(t, "delete", "Acme/general", "500500")
	if code != 0 || !strings.Contains(out, "removed from Acme/general") {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
}

func TestEmptyDirectMessagesStateTheZero(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t, "dms")
	if code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "dms: 0 direct message channels") {
		t.Fatalf("an empty list must say so explicitly:\n%s", out)
	}
}

func TestSearchExplainsTheBotLimitInsteadOfLeakingA403(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	t.Setenv(TokenEnvVar, BotPrefix+"test-bot-token")

	out, code := run(t, "search", "Acme", "--content", "deploy")
	if code != 1 || !strings.Contains(out, "does not offer message search to bot accounts") {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if strings.Contains(out, "arikawa") || strings.Contains(out, "discordo") {
		t.Fatalf("errors must not leak dependency names:\n%s", out)
	}
}

func TestUnreadRefusesBotTokensWithoutConnecting(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	t.Setenv(TokenEnvVar, BotPrefix+"test-bot-token")

	out, code := run(t, "unread")
	if code != 1 || !strings.Contains(out, "read state for user accounts only") {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
}

func TestLoggedOutHomeAsksForCredentials(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	t.Setenv(TokenEnvVar, "")
	t.Setenv("PATH", "") // keeps the keyring helper from answering on this machine

	out, code := run(t)
	if code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "account: not logged in") || !strings.Contains(out, "login --token") {
		t.Fatalf("logged out home should ask for a token:\n%s", out)
	}
}

func TestUnknownFieldNamesTheAvailableOnes(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t, "messages", "Acme/general", "--fields", "reactions,bogus")
	if code != 2 || !strings.Contains(out, `unknown field \"bogus\"`) {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "attachments, edited, pinned, reactions, replyTo, url") {
		t.Fatalf("error should list the available fields:\n%s", out)
	}
}

// Discord's REST message payload omits guild_id, so the jump url has to take it
// from the resolved channel or it points at @me and does not open.
func TestJumpURLsPointAtTheGuildNotAtDirectMessages(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t, "messages", "Acme/general", "--fields", "url")
	if code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if strings.Contains(out, "/channels/@me/") {
		t.Fatalf("guild message urls must carry the guild id:\n%s", out)
	}
	if !strings.Contains(out, "https://discord.com/channels/"+guildID+"/"+generalID+"/300") {
		t.Fatalf("url is missing or malformed:\n%s", out)
	}
}

func TestRequestedFieldsAreAppendedToTheSchema(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t, "messages", "Acme/general", "--fields", "url")
	if code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if !strings.Contains(out, "messages[2]{id,author,time,content,url}:") {
		t.Fatalf("--fields should extend the row schema:\n%s", out)
	}
}

// A usage error must not be hidden behind a missing account: the agent would
// fix the wrong thing.
func TestArgvIsValidatedBeforeTheAccountIsResolved(t *testing.T) {
	var requests []string
	mockDiscord(t, discordAPI(t, &requests))
	t.Setenv(TokenEnvVar, "")
	t.Setenv("PATH", "") // keeps the keyring helper from answering on this machine

	cases := [][]string{
		{"guilds", "--limit", "abc"},
		{"messages", "Acme/general", "--limit", "101"},
		{"messages", "Acme/general", "--fields", "nope"},
		{"delete", "Acme/general", "not-a-snowflake"},
		{"send", "Acme/general", "--content", "hi", "--reply", "not-a-snowflake"},
		{"search", "Acme", "--content", "x", "--limit", "0"},
	}
	for _, argv := range cases {
		out, code := run(t, argv...)
		if code != 2 {
			t.Fatalf("%v exited %d, want a usage error:\n%s", argv, code, out)
		}
	}
	if len(requests) != 0 {
		t.Fatalf("validation must happen before any request, got %v", requests)
	}
}

func TestLimitSuggestionsUseTheCommandsOwnDefault(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, _ := run(t, "guilds", "--limit", "abc")
	if !strings.Contains(out, "guilds --limit "+defaultListLimit) {
		t.Fatalf("suggestion should offer the command's default:\n%s", out)
	}
	out, _ = run(t, "messages", "Acme/general", "--limit", "abc")
	if !strings.Contains(out, "messages --limit "+defaultMsgLimit) {
		t.Fatalf("suggestion should offer the command's default:\n%s", out)
	}
}

func TestMissingActionNamesTheActions(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	for command, actions := range map[string]string{
		"auth":   "list, use, scope",
		"setup":  "hooks, skill",
		"daemon": "start, run, status, stop",
	} {
		out, code := run(t, command)
		if code != 2 || !strings.Contains(out, actions) {
			t.Fatalf("`%s` with no action exited %d and did not list %q:\n%s", command, code, actions, out)
		}
	}
}

func TestOptionalArgumentsReadAsOptional(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t, "logout", "--help")
	if code != 0 || !strings.Contains(out, "discord-axi logout [account]") {
		t.Fatalf("optional arguments belong in brackets:\n%s", out)
	}
}

// `setup skill` regenerates this repository's file; it is not how a user
// installs the skill, and saying so was the whole confusion.
func TestSetupSkillPointsAtTheRealInstallCommand(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := run(t, "setup", "skill", "--path", filepath.Join(t.TempDir(), "SKILL.md"))
	if code != 0 {
		t.Fatalf("exit code = %d:\n%s", code, out)
	}
	if strings.Contains(out, "<owner>") || !strings.Contains(out, SkillInstallCommand) {
		t.Fatalf("help should name this repository, not a placeholder:\n%s", out)
	}
}

func TestSkillTellsAnAgentHowToGetTheBinary(t *testing.T) {
	if !strings.Contains(SkillMarkdown(), InstallCommand) {
		t.Fatal("a skill can be installed without the binary, so it must say how to get one")
	}
}

// Password login was removed because it is what got accounts disabled
// upstream. Any surviving mention of it would send an agent down a dead end.
func TestNothingStillAdvertisesPasswordLogin(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	t.Setenv(TokenEnvVar, "")
	t.Setenv("PATH", "") // keeps the keyring helper from answering on this machine

	home, _ := run(t)
	surfaces := map[string]string{"logged-out home": home, "skill": SkillMarkdown()}
	for _, command := range Commands() {
		out, _ := run(t, command.Name, "--help")
		surfaces[command.Name+" --help"] = out
	}
	for where, text := range surfaces {
		for _, gone := range []string{"--password", "--email"} {
			if strings.Contains(text, gone) {
				t.Fatalf("%s still offers %s:\n%s", where, gone, text)
			}
		}
	}
}
