package app

import (
	"net/http"
	"strings"
	"testing"
)

// login stores a file-backed account so no test touches the system keyring.
func login(t *testing.T, args ...string) (string, int) {
	t.Helper()
	t.Setenv(TokenEnvVar, "")
	return run(t, append([]string{"login", "--store", StoreFile}, args...)...)
}

func TestLoginKeepsUserTokensReadOnlyUntilAsked(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	out, code := login(t, "--token", "user-token", "--as", "personal")
	if code != 0 {
		t.Fatalf("login failed with %d: %s", code, out)
	}
	for _, want := range []string{"kind: user", "scope: read", "store: file"} {
		if !strings.Contains(out, want) {
			t.Fatalf("login output missing %q: %s", want, out)
		}
	}

	index := LoadIndex()
	if index.Default != "personal" {
		t.Fatalf("first account did not become the default: %+v", index)
	}
	if index.Profiles["personal"].Username != "axi" {
		t.Fatalf("profile did not record the Discord username: %+v", index.Profiles)
	}
}

func TestReadOnlyAccountRefusesToSendWithoutReachingDiscord(t *testing.T) {
	var requests []string
	mockDiscord(t, discordAPI(t, &requests))
	login(t, "--token", "user-token", "--as", "personal")

	before := len(requests)
	out, code := run(t, "send", "Acme/general", "--content", "hi", "--account", "personal")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "READ_ONLY") {
		t.Fatalf("error did not name the scope: %s", out)
	}
	for _, request := range requests[before:] {
		if strings.HasPrefix(request, http.MethodPost) {
			t.Fatalf("a read-only account reached Discord with %q", request)
		}
	}
}

func TestWideningTheScopeLetsTheSameAccountSend(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	login(t, "--token", "user-token", "--as", "personal")

	if out, code := run(t, "auth", "scope", "personal", "--write"); code != 0 {
		t.Fatalf("auth scope failed with %d: %s", code, out)
	}
	out, code := run(t, "send", "Acme/general", "--content", "hi", "--account", "personal")
	if code != 0 {
		t.Fatalf("send failed with %d: %s", code, out)
	}
	if !strings.Contains(out, "999") {
		t.Fatalf("send did not report the message id: %s", out)
	}
}

func TestRepeatedScopeChangeIsANoOp(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	login(t, "--token", "user-token", "--as", "personal")

	out, code := run(t, "auth", "scope", "personal", "--read")
	if code != 0 || !strings.Contains(out, "no-op") {
		t.Fatalf("expected an idempotent no-op, got %d: %s", code, out)
	}
}

func TestScopeNeedsExactlyOneDirection(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	login(t, "--token", "user-token", "--as", "personal")

	out, code := run(t, "auth", "scope", "personal")
	if code != 2 {
		t.Fatalf("expected a usage error, got %d: %s", code, out)
	}
	if !strings.Contains(out, "--read") || !strings.Contains(out, "--write") {
		t.Fatalf("error did not name both directions: %s", out)
	}
}

func TestNamedAccountBeatsATokenInTheEnvironment(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	login(t, "--token", "stored-token", "--as", "personal")

	t.Setenv(TokenEnvVar, "environment-token")
	out, code := run(t, "whoami", "--account", "personal")
	if code != 0 {
		t.Fatalf("whoami failed with %d: %s", code, out)
	}
	if !strings.Contains(out, "profile: personal") || !strings.Contains(out, "scope: read") {
		t.Fatalf("whoami did not report the named account: %s", out)
	}
}

func TestUnknownAccountListsTheStoredOnes(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	login(t, "--token", "user-token", "--as", "personal")

	out, code := run(t, "guilds", "--account", "work")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d: %s", code, out)
	}
	if !strings.Contains(out, "known accounts: personal") {
		t.Fatalf("error did not list the stored accounts: %s", out)
	}
}

func TestLogoutForgetsTheAccountAndItsSecret(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	login(t, "--token", "user-token", "--as", "personal")

	if out, code := run(t, "logout", "personal"); code != 0 {
		t.Fatalf("logout failed with %d: %s", code, out)
	}
	if _, err := readSecret("personal", StoreFile); err == nil {
		t.Fatal("the secret survived logout")
	}
	out, code := run(t, "logout", "personal")
	if code != 0 || !strings.Contains(out, "no-op") {
		t.Fatalf("a second logout was not a no-op: %d %s", code, out)
	}
}

func TestUnknownStoreIsRejectedBeforeContactingDiscord(t *testing.T) {
	var requests []string
	mockDiscord(t, discordAPI(t, &requests))

	t.Setenv(TokenEnvVar, "")
	out, code := run(t, "login", "--token", "user-token", "--store", "vault")
	if code != 2 {
		t.Fatalf("expected a usage error, got %d: %s", code, out)
	}
	if len(requests) != 0 {
		t.Fatalf("an invalid --store still reached Discord: %v", requests)
	}
}

func TestDaemonReadsTheAccountOutOfRawArgv(t *testing.T) {
	cases := map[string]string{
		"":                                 "",
		"unread --account personal":        "personal",
		"unread --account=personal":        "personal",
		"unread --mentions --account=work": "work",
		"unread --account":                 "",
		"unread --accounts personal":       "",
	}
	for argv, want := range cases {
		inv := accountFromArgs(strings.Fields(argv))
		got := ""
		if inv != nil {
			got = inv.String(AccountFlag)
		}
		if got != want {
			t.Fatalf("%q resolved to account %q, want %q", argv, got, want)
		}
	}
}

// A token in the environment is the one path that could otherwise slip past the
// scope a stored account carries, so it follows the same rule `login` follows.
func TestEnvironmentTokensFollowTheSameScopeRuleAsLogin(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))

	for _, want := range []struct {
		token, scope, kind string
	}{
		{"user-token", ScopeRead, KindUser},
		{BotPrefix + "bot-token", ScopeWrite, KindBot},
	} {
		t.Setenv(TokenEnvVar, want.token)
		t.Setenv(ScopeEnvVar, "")
		credentials, err := Resolve(nil)
		if err != nil {
			t.Fatalf("%s: %v", want.kind, err)
		}
		if credentials.Kind != want.kind || credentials.Scope != want.scope {
			t.Fatalf("%s token resolved to %s/%s, want %s/%s",
				want.kind, credentials.Kind, credentials.Scope, want.kind, want.scope)
		}
	}
}

func TestEnvironmentScopeCanWidenAndIsValidated(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	t.Setenv(TokenEnvVar, "user-token")

	t.Setenv(ScopeEnvVar, ScopeWrite)
	credentials, err := Resolve(nil)
	if err != nil || credentials.Scope != ScopeWrite {
		t.Fatalf("scope = %q, err = %v, want an explicit widening to work", credentials.Scope, err)
	}

	t.Setenv(ScopeEnvVar, "readonly")
	out, code := run(t, "guilds")
	if code != 2 {
		t.Fatalf("an unreadable scope must be a usage error, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, ScopeEnvVar) {
		t.Fatalf("the error must name the variable:\n%s", out)
	}
}

// `auth scope env --write` would fail: the environment is not a stored account.
func TestReadOnlyEnvironmentTokenIsToldHowToWidenItself(t *testing.T) {
	mockDiscord(t, discordAPI(t, nil))
	t.Setenv(TokenEnvVar, "user-token")

	out, code := run(t, "send", "Acme/general", "--content", "hi")
	if code != 1 || !strings.Contains(out, ScopeEnvVar+"="+ScopeWrite) {
		t.Fatalf("exit %d; the hint must fit how the token arrived:\n%s", code, out)
	}
	if strings.Contains(out, "auth scope "+envProfile) {
		t.Fatalf("suggested a command that cannot work:\n%s", out)
	}
}
