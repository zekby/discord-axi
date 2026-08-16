package app

import (
	"errors"
	"os"
	"strconv"

	"github.com/zekby/discord-axi/internal/axi"
)

// Description is the one sentence that identifies this CLI everywhere.
const Description = "Read and write Discord from the shell, in agent-readable TOON"

// Home is the no-argument view: live content first, never a manual. It stays
// REST-only so a session-start hook pays no gateway latency.
func Home() (*axi.Doc, error) {
	client, credentials, err := Client(nil)
	if err != nil {
		return axi.NewDoc().
			Set("account", "not logged in").
			Set("help", []string{
				"Run `" + axi.Binary() + ` login --token "<token>"` + "` to authenticate",
				"Run `" + axi.Binary() + ` login --email "<email>" --password "<password>"` + "` to log in with a password",
				"Or set " + TokenEnvVar + " in the environment",
			}), nil
	}

	me, err := client.Me()
	if err != nil {
		markInvalid(credentials.Profile, true)
		return nil, Translate(err)
	}
	markInvalid(credentials.Profile, false)
	guilds, err := client.Guilds(0)
	if err != nil {
		return nil, Translate(err)
	}

	doc := axi.NewDoc().Set("account",
		me.DisplayOrUsername()+" ("+credentials.Profile+", "+credentials.Kind+", "+credentials.Scope+")")
	if len(guilds) == 0 {
		doc.Set("guilds", "0 guilds found for this account")
	} else {
		shown := guilds
		if len(shown) > homeGuilds {
			shown = shown[:homeGuilds]
		}
		rows := make([]*axi.Doc, 0, len(shown))
		for _, guild := range shown {
			rows = append(rows, axi.NewDoc().Set("id", guild.ID.String()).Set("name", guild.Name))
		}
		doc.Set("count", countOf(len(shown), len(guilds))).Set("guilds", rows)
	}

	help := []string{
		"Run `" + axi.Binary() + ` messages "<guild>/<channel>"` + "` to read a channel",
		"Run `" + axi.Binary() + ` send "<guild>/<channel>" --content "<text>"` + "` to post",
	}
	if len(guilds) > homeGuilds {
		help = append([]string{
			"Run `" + axi.Binary() + " guilds` for all " + strconv.Itoa(len(guilds)) + " guilds",
		}, help...)
	}
	if !credentials.IsBot() && credentials.Scope == ScopeWrite {
		help = append(help,
			"Run `"+axi.Binary()+" unread` to see unread channels and mentions",
			// The session hook shows this before the agent acts, which is the only
			// moment the warning is worth anything.
			"This is a user token: send, edit, delete, react and read write as the account and risk it being disabled — confirm with the user before writing")
	}
	return doc.Set("help", help), nil
}

// errorHelp reuses the suggestions an auth failure already carries, so the home
// view says the same thing every other command would.
func errorHelp(err error) []string {
	var axiErr *axi.Error
	if errors.As(err, &axiErr) && len(axiErr.Help) > 0 {
		return axiErr.Help
	}
	return []string{"Run `" + axi.Binary() + ` login --token "<token>"` + "` to authenticate"}
}

// App wires the command surface into the AXI runtime.
func App(version string) *axi.App {
	return &axi.App{
		Name:        "discord-axi",
		Description: Description,
		Version:     version,
		Home:        Home,
		Commands:    Commands(),
		GlobalFlags: []axi.Flag{AccountGlobal()},
		HelpNotes: []string{
			"Run `discord-axi` with no arguments to see the account and its guilds",
			"Run `discord-axi auth list` to see every stored account and what it may do",
			"Run `discord-axi <command> --help` for the flags and examples of one command",
			"A channel is a snowflake id, a `<guild>/<channel>` path, or a direct message name",
		},
	}
}

func setupCommand() *axi.Command {
	return &axi.Command{
		Name: "setup",
		Desc: "Install the session-start hooks, or write the installable skill file",
		Args: []axi.Arg{{Name: "target", Required: true}},
		Flags: []axi.Flag{
			{Name: "--path", Value: "path", Desc: "Where `setup skill` writes SKILL.md", Default: "SKILL.md"},
			{Name: "--check", Desc: "For `setup skill`: fail if the file is stale instead of writing it"},
		},
		Examples: []string{"discord-axi setup hooks", "discord-axi setup skill --path SKILL.md"},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			switch inv.Arg(0) {
			case "hooks":
				return installHooks()
			case "skill":
				return writeSkill(inv.String("--path"), inv.Bool("--check"))
			default:
				return nil, axi.Usage(`unknown setup target "`+inv.Arg(0)+`"`,
					"Run `"+axi.Binary()+" setup hooks` to register session-start context",
					"Run `"+axi.Binary()+" setup skill` to write SKILL.md")
			}
		},
	}
}

func installHooks() (*axi.Doc, error) {
	results := axi.InstallSessionStartHooks(axi.Binary())
	rows := make([]*axi.Doc, 0, len(results))
	failures := 0
	for _, result := range results {
		status := "already up to date"
		switch {
		case result.Err != nil:
			status = "failed: " + result.Err.Error()
			failures++
		case result.Changed:
			status = "installed"
		}
		rows = append(rows, axi.NewDoc().
			Set("app", result.App).
			Set("path", axi.CollapseHome(result.Path)).
			Set("status", status))
	}

	doc := axi.NewDoc().Set("setup", "session-start hooks for "+axi.Binary()).Set("targets", rows)
	if failures == len(results) {
		return nil, axi.Fail("SETUP_FAILED", "no session-start hook could be installed",
			"Check write permission on ~/.claude, ~/.codex and ~/.config/opencode")
	}
	return doc.Set("help", []string{
		"Start a new agent session to see the Discord home view in its initial context",
		"Run `" + axi.Binary() + " setup skill` to also install the on-demand skill file",
	}), nil
}

func writeSkill(path string, check bool) (*axi.Doc, error) {
	content := SkillMarkdown()
	if check {
		current, err := os.ReadFile(path)
		if err != nil || string(current) != content {
			return nil, axi.Usage(axi.CollapseHome(path)+" is stale",
				"Run `"+axi.Binary()+" setup skill --path "+path+"` and commit the result")
		}
		return axi.NewDoc().Set("skill", axi.CollapseHome(path)+" is up to date"), nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, axi.Fail("WRITE_FAILED", "could not write "+path,
			"Pass a writable location with --path")
	}
	return axi.NewDoc().
		Set("skill", "wrote "+axi.CollapseHome(path)).
		Set("help", []string{"Commit the file so agents can install it with `npx skills add <owner>/<repo>`"}), nil
}
