package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zekby/discord-axi/internal/axi"
	"github.com/zekby/discord-axi/internal/discordo/keyring"
)

// Environment overrides, in the order they are consulted.
const (
	// AccountEnvVar selects a stored profile without passing --account.
	AccountEnvVar = "DISCORD_AXI_ACCOUNT"
	// TokenEnvVar supplies a token directly, for CI and one-off shells.
	TokenEnvVar = "DISCORD_AXI_TOKEN"
	// ScopeEnvVar restricts a token taken from the environment.
	ScopeEnvVar = "DISCORD_AXI_SCOPE"
)

// Scopes. A read profile cannot mutate anything on Discord, enforced at the
// HTTP layer rather than per command, so a new command cannot forget the check.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// Token kinds. Discord expects a bot token behind a "Bot " prefix and a user
// token bare, so the kind is stored beside the secret instead of being baked
// into it.
const (
	KindBot  = "bot"
	KindUser = "user"
)

// Secret stores. The keyring is the default; a file is the honest fallback for
// machines without one, and which was used is always reported.
const (
	StoreKeyring = "keyring"
	StoreFile    = "file"
)

// envProfile is the name reported when the token came from the environment
// rather than from a stored profile.
const envProfile = "env"

// Profile is everything about an account except its secret.
type Profile struct {
	Kind     string `json:"kind"`
	Scope    string `json:"scope"`
	Store    string `json:"store"`
	ID       string `json:"id"`
	Username string `json:"username"`
	AddedAt  int64  `json:"addedAt"`
	// Invalid records that Discord rejected the token, so the home view can say
	// so without spending a request to find out again.
	Invalid bool `json:"invalid,omitempty"`
}

// ProfileIndex is the on-disk registry. It never holds a token.
type ProfileIndex struct {
	Default  string             `json:"default"`
	Profiles map[string]Profile `json:"profiles"`
}

func indexPath() string { return filepath.Join(StateDir(), "profiles.json") }

func secretPath(name string) string {
	return filepath.Join(StateDir(), "secrets", name)
}

func LoadIndex() ProfileIndex {
	index := ProfileIndex{Profiles: map[string]Profile{}}
	raw, err := os.ReadFile(indexPath())
	if err != nil {
		return index
	}
	if err := json.Unmarshal(raw, &index); err != nil || index.Profiles == nil {
		return ProfileIndex{Profiles: map[string]Profile{}}
	}
	return index
}

func saveIndex(index ProfileIndex) error {
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath(), append(encoded, '\n'), 0o600)
}

func (i ProfileIndex) Names() []string {
	names := make([]string, 0, len(i.Profiles))
	for name := range i.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Credentials is a resolved account: the raw token plus how it must be
// presented and what it is allowed to do.
type Credentials struct {
	Profile string
	Token   string
	Kind    string
	Scope   string
	Source  string
}

func (c Credentials) IsBot() bool { return c.Kind == KindBot }

func (c Credentials) ReadOnly() bool { return c.Scope == ScopeRead }

// Authorization is the header value Discord expects for this kind of token.
func (c Credentials) Authorization() string {
	if c.IsBot() {
		return BotPrefix + c.Token
	}
	return c.Token
}

// BotPrefix is what Discord expects in front of a bot token.
const BotPrefix = "Bot "

func storeSecret(name, token, store string) error {
	if store == StoreFile {
		if err := os.MkdirAll(filepath.Dir(secretPath(name)), 0o700); err != nil {
			return err
		}
		return os.WriteFile(secretPath(name), []byte(token), 0o600)
	}
	return keyring.SetToken(name, token)
}

func readSecret(name, store string) (string, error) {
	if store == StoreFile {
		raw, err := os.ReadFile(secretPath(name))
		return strings.TrimSpace(string(raw)), err
	}
	return keyring.GetToken(name)
}

func deleteSecret(name, store string) {
	if store == StoreFile {
		_ = os.Remove(secretPath(name))
		return
	}
	_ = keyring.DeleteToken(name)
}

// Resolve decides which account a command runs as: an explicit --account, then
// DISCORD_AXI_ACCOUNT, then a token in the environment, then the default
// profile. The order puts anything the caller stated ahead of anything ambient.
func Resolve(inv *axi.Invocation) (Credentials, error) {
	index := LoadIndex()

	requested := ""
	if inv != nil {
		requested = inv.String(AccountFlag)
	}
	if requested == "" {
		requested = os.Getenv(AccountEnvVar)
	}

	if requested != "" {
		profile, ok := index.Profiles[requested]
		if !ok {
			return Credentials{}, axi.Fail("NOT_FOUND", `no account named "`+requested+`"`,
				knownAccounts(index),
				"Run `"+axi.Binary()+` login --token "<token>" --as `+requested+"` to add it")
		}
		token, err := readSecret(requested, profile.Store)
		if err != nil || token == "" {
			return Credentials{}, axi.Fail("NOT_AUTHENTICATED",
				`the secret for account "`+requested+`" is missing from the `+profile.Store,
				"Run `"+axi.Binary()+` login --token "<token>" --as `+requested+"` to store it again")
		}
		return Credentials{
			Profile: requested,
			Token:   token,
			Kind:    profile.Kind,
			Scope:   profile.Scope,
			Source:  profile.Store + " profile " + requested,
		}, nil
	}

	if token := os.Getenv(TokenEnvVar); token != "" {
		kind := KindUser
		if strings.HasPrefix(token, BotPrefix) {
			kind = KindBot
			token = strings.TrimPrefix(token, BotPrefix)
		}
		scope, err := envScope(kind)
		if err != nil {
			return Credentials{}, err
		}
		return Credentials{
			Profile: envProfile,
			Token:   token,
			Kind:    kind,
			Scope:   scope,
			Source:  TokenEnvVar + " environment variable",
		}, nil
	}

	if index.Default != "" {
		if profile, ok := index.Profiles[index.Default]; ok {
			token, err := readSecret(index.Default, profile.Store)
			if err == nil && token != "" {
				return Credentials{
					Profile: index.Default,
					Token:   token,
					Kind:    profile.Kind,
					Scope:   profile.Scope,
					Source:  profile.Store + " profile " + index.Default,
				}, nil
			}
		}
	}

	help := []string{
		"Run `" + axi.Binary() + ` login --token "<token>"` + "` to authenticate",
		"Or set " + TokenEnvVar + " in the environment",
	}
	if len(index.Profiles) > 0 {
		help = append([]string{knownAccounts(index),
			"Run `" + axi.Binary() + " auth use <name>` to pick a default"}, help...)
	}
	return Credentials{}, axi.Fail("NOT_AUTHENTICATED", "not logged in", help...)
}

// envScope applies the rule `login` applies: a user token may only read unless
// the caller widens it in as many words. A token in the environment is the one
// path that could otherwise slip past the scope a stored account would carry.
func envScope(kind string) (string, error) {
	switch requested := os.Getenv(ScopeEnvVar); requested {
	case ScopeRead, ScopeWrite:
		return requested, nil
	case "":
		if kind == KindUser {
			return ScopeRead, nil
		}
		return ScopeWrite, nil
	default:
		return "", axi.Usage(ScopeEnvVar+` must be "`+ScopeRead+`" or "`+ScopeWrite+`", got "`+requested+`"`,
			"Set "+ScopeEnvVar+"="+ScopeWrite+" to allow writes with the token in "+TokenEnvVar,
			"Unset "+ScopeEnvVar+" to let the token kind decide")
	}
}

func knownAccounts(index ProfileIndex) string {
	if len(index.Profiles) == 0 {
		return "no accounts are stored yet"
	}
	return "known accounts: " + strings.Join(index.Names(), ", ")
}

// markInvalid remembers that Discord rejected a profile's token, so the next
// command can say so before spending a request.
func markInvalid(name string, invalid bool) {
	if name == "" || name == envProfile {
		return
	}
	index := LoadIndex()
	profile, ok := index.Profiles[name]
	if !ok || profile.Invalid == invalid {
		return
	}
	profile.Invalid = invalid
	index.Profiles[name] = profile
	_ = saveIndex(index)
}

func upsertProfile(name string, profile Profile, makeDefault bool) error {
	index := LoadIndex()
	profile.AddedAt = time.Now().Unix()
	index.Profiles[name] = profile
	if makeDefault || index.Default == "" {
		index.Default = name
	}
	return saveIndex(index)
}

func removeProfile(name string) bool {
	index := LoadIndex()
	profile, ok := index.Profiles[name]
	if !ok {
		return false
	}
	deleteSecret(name, profile.Store)
	delete(index.Profiles, name)
	if index.Default == name {
		index.Default = ""
		for _, remaining := range index.Names() {
			index.Default = remaining
			break
		}
	}
	_ = saveIndex(index)
	return true
}
