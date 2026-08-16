package app

import (
	"strings"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/zekby/discord-axi/internal/axi"
)

func loginCommand() *axi.Command {
	return &axi.Command{
		Name: "login",
		Desc: "Verify a token and store it as a named account",
		Flags: []axi.Flag{
			{Name: "--token", Value: "token", Desc: "Bot or user token", Required: true},
			{Name: "--as", Value: "name", Desc: "Account name; defaults to the Discord username"},
			{Name: "--scope", Value: "read|write", Desc: "What this account may do; user tokens default to read"},
			{Name: "--store", Value: "keyring|file", Desc: "Where to keep the secret", Default: StoreKeyring},
			{Name: "--default", Desc: "Make this the account used when none is named"},
		},
		Examples: []string{
			`discord-axi login --token "<bot-token>" --as work --scope write`,
			`discord-axi login --token "<user-token>" --as personal`,
		},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			store := inv.String("--store")
			if store != StoreKeyring && store != StoreFile {
				return nil, axi.Usage(`--store must be "`+StoreKeyring+`" or "`+StoreFile+`", got "`+store+`"`,
					"Run `"+axi.Binary()+` login --token "<token>" --store keyring`+"`",
					"Use --store file on a machine with no usable keyring")
			}
			scope := inv.String("--scope")
			if scope != "" && scope != ScopeRead && scope != ScopeWrite {
				return nil, axi.Usage(`--scope must be "`+ScopeRead+`" or "`+ScopeWrite+`", got "`+scope+`"`,
					"Run `"+axi.Binary()+` login --token "<token>" --scope read`+"`")
			}

			token, kind, me, err := verify(inv.String("--token"))
			if err != nil {
				return nil, err
			}

			// A user token is the one that can get an account disabled, so it is
			// read-only unless the caller says otherwise in as many words.
			if scope == "" {
				scope = ScopeWrite
				if kind == KindUser {
					scope = ScopeRead
				}
			}

			name := inv.String("--as")
			if name == "" {
				name = me.Username
			}
			if strings.ContainsAny(name, "/\\ ") || name == "" {
				return nil, axi.Usage(`--as must be a plain name without spaces or slashes, got "`+name+`"`,
					"Run `"+axi.Binary()+` login --token "<token>" --as work`+"`")
			}

			if err := storeSecret(name, token, store); err != nil {
				if store == StoreKeyring {
					return nil, axi.Fail("KEYRING_ERROR", "the system keyring refused to store the token",
						"Run `"+axi.Binary()+` login --token "<token>" --as `+name+" --store file` to keep it in a 0600 file instead")
				}
				return nil, axi.Fail("STORE_ERROR", "could not write the token to "+axi.CollapseHome(secretPath(name)))
			}
			profile := Profile{
				Kind:     kind,
				Scope:    scope,
				Store:    store,
				ID:       me.ID.String(),
				Username: me.Username,
			}
			if err := upsertProfile(name, profile, inv.Bool("--default")); err != nil {
				return nil, axi.Fail("STORE_ERROR", "could not record the account")
			}

			doc := axi.NewDoc().
				Set("login", "stored "+me.DisplayOrUsername()+" as account "+name).
				Set("kind", kind).
				Set("scope", scope).
				Set("store", store).
				Set("id", me.ID.String())
			help := []string{"Run `" + axi.Binary() + " auth list` to see every stored account"}
			if scope == ScopeRead {
				help = append(help,
					"This account cannot write; run `"+axi.Binary()+" auth scope "+name+" --write` if that is wanted")
			}
			return doc.Set("help", help), nil
		},
	}
}

// verify decides how a token must be presented. A bot token is only valid behind
// the "Bot " prefix and a user token only without it, so both are tried once.
func verify(raw string) (string, string, *discord.User, error) {
	token := strings.TrimPrefix(raw, BotPrefix)
	for _, kind := range []string{KindUser, KindBot} {
		credentials := Credentials{Token: token, Kind: kind, Scope: ScopeRead, Profile: envProfile}
		me, err := NewClient(credentials).Me()
		if err == nil {
			return token, kind, me, nil
		}
		if !isUnauthorized(err) {
			return "", "", nil, Translate(err)
		}
	}
	return "", "", nil, axi.Fail("NOT_AUTHENTICATED", "token rejected by Discord",
		"Check the token was copied in full and has not been reset")
}

func logoutCommand() *axi.Command {
	return &axi.Command{
		Name:     "logout",
		Desc:     "Forget a stored account and its secret",
		Args:     []axi.Arg{{Name: "account", Required: false}},
		Examples: []string{"discord-axi logout", "discord-axi logout personal"},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			index := LoadIndex()
			name := inv.Arg(0)
			if name == "" {
				name = inv.String(AccountFlag)
			}
			if name == "" {
				name = index.Default
			}
			if name == "" {
				return axi.NewDoc().Set("logout", "no accounts are stored (no-op)"), nil
			}
			if !removeProfile(name) {
				return axi.NewDoc().
					Set("logout", `no account named "`+name+`" (no-op)`).
					Set("help", []string{knownAccounts(index)}), nil
			}
			doc := axi.NewDoc().Set("logout", "account "+name+" removed")
			if remaining := LoadIndex(); remaining.Default != "" {
				doc.Set("default", remaining.Default)
			}
			return doc, nil
		},
	}
}

func whoamiCommand() *axi.Command {
	return &axi.Command{
		Name:     "whoami",
		Desc:     "Show which account a command would run as",
		Examples: []string{"discord-axi whoami", "discord-axi whoami --account personal"},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			client, credentials, err := Client(inv)
			if err != nil {
				return nil, err
			}
			me, err := client.Me()
			if err != nil {
				markInvalid(credentials.Profile, true)
				return nil, Translate(err)
			}
			markInvalid(credentials.Profile, false)
			return axi.NewDoc().
				Set("account", me.DisplayOrUsername()).
				Set("profile", credentials.Profile).
				Set("id", me.ID.String()).
				Set("username", me.Username).
				Set("kind", credentials.Kind).
				Set("scope", credentials.Scope).
				Set("source", credentials.Source), nil
		},
	}
}

func authCommand() *axi.Command {
	return &axi.Command{
		Name: "auth",
		Desc: "List stored accounts, pick the default, or change what one may do",
		Args: []axi.Arg{{Name: "action", Required: true}, {Name: "account", Required: false}},
		Flags: []axi.Flag{
			{Name: "--read", Desc: "For `auth scope`: forbid writes"},
			{Name: "--write", Desc: "For `auth scope`: allow writes"},
		},
		Examples: []string{
			"discord-axi auth list",
			"discord-axi auth use personal",
			"discord-axi auth scope personal --write",
		},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			switch inv.Arg(0) {
			case "list":
				return authList()
			case "use":
				return authUse(inv.Arg(1))
			case "scope":
				return authScope(inv)
			default:
				return nil, axi.Usage(`unknown auth action "`+inv.Arg(0)+`"`,
					"Run `"+axi.Binary()+" auth list` to see stored accounts",
					"Run `"+axi.Binary()+" auth use <account>` to pick the default",
					"Run `"+axi.Binary()+" auth scope <account> --read|--write` to change what it may do")
			}
		},
	}
}

func authList() (*axi.Doc, error) {
	index := LoadIndex()
	if len(index.Profiles) == 0 {
		return axi.NewDoc().
			Set("accounts", "0 accounts stored").
			Set("help", []string{
				"Run `" + axi.Binary() + ` login --token "<token>" --as <name>` + "` to add one",
			}), nil
	}

	rows := make([]*axi.Doc, 0, len(index.Profiles))
	for _, name := range index.Names() {
		profile := index.Profiles[name]
		state := "ok"
		if profile.Invalid {
			state = "token rejected, log in again"
		}
		rows = append(rows, axi.NewDoc().
			Set("name", name).
			Set("account", profile.Username).
			Set("kind", profile.Kind).
			Set("scope", profile.Scope).
			Set("store", profile.Store).
			Set("default", name == index.Default).
			Set("state", state))
	}
	return axi.NewDoc().
		Set("count", countOf(len(rows), len(rows))).
		Set("accounts", rows).
		Set("help", []string{
			"Run `" + axi.Binary() + " <command> --account <name>` to use one for a single command",
			"Run `" + axi.Binary() + " auth use <name>` to change the default",
		}), nil
}

func authUse(name string) (*axi.Doc, error) {
	if name == "" {
		return nil, axi.Usage("<account> is required for `auth use`",
			"Run `"+axi.Binary()+" auth list` to see stored accounts")
	}
	index := LoadIndex()
	if _, ok := index.Profiles[name]; !ok {
		return nil, axi.Fail("NOT_FOUND", `no account named "`+name+`"`, knownAccounts(index))
	}
	if index.Default == name {
		return axi.NewDoc().Set("auth", name+" is already the default (no-op)"), nil
	}
	index.Default = name
	if err := saveIndex(index); err != nil {
		return nil, axi.Fail("STORE_ERROR", "could not record the default account")
	}
	return axi.NewDoc().Set("auth", "default account is now "+name), nil
}

func authScope(inv *axi.Invocation) (*axi.Doc, error) {
	name := inv.Arg(1)
	if name == "" {
		name = LoadIndex().Default
	}
	if name == "" {
		return nil, axi.Usage("<account> is required for `auth scope`",
			"Run `"+axi.Binary()+" auth scope <account> --write`")
	}

	wantRead, wantWrite := inv.Bool("--read"), inv.Bool("--write")
	if wantRead == wantWrite {
		return nil, axi.Usage("pass exactly one of --read or --write",
			"Run `"+axi.Binary()+" auth scope "+name+" --write` to allow writes",
			"Run `"+axi.Binary()+" auth scope "+name+" --read` to forbid them")
	}
	scope := ScopeRead
	if wantWrite {
		scope = ScopeWrite
	}

	index := LoadIndex()
	profile, ok := index.Profiles[name]
	if !ok {
		return nil, axi.Fail("NOT_FOUND", `no account named "`+name+`"`, knownAccounts(index))
	}
	if profile.Scope == scope {
		return axi.NewDoc().Set("auth", name+" is already "+scope+" (no-op)"), nil
	}
	profile.Scope = scope
	index.Profiles[name] = profile
	if err := saveIndex(index); err != nil {
		return nil, axi.Fail("STORE_ERROR", "could not record the scope")
	}

	doc := axi.NewDoc().Set("auth", name+" is now "+scope)
	if scope == ScopeWrite && profile.Kind == KindUser {
		doc.Set("caution", WriteCaution)
	}
	return doc, nil
}
