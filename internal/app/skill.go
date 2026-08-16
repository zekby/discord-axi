package app

import (
	"strings"

	"github.com/zekby/discord-axi/internal/axi"
)

// isAccountCommand marks the commands that manage this CLI rather than reach for
// Discord content, so the ban-risk section talks about reads and writes only.
func isAccountCommand(name string) bool {
	switch name {
	case "login", "logout", "whoami", "auth", "setup", "daemon":
		return true
	}
	return false
}

// Repository is where the skill and the source live. It is the one place the
// owner/name pair is written down, so no suggestion prints a placeholder.
const Repository = "zekby/discord-axi"

// SkillInstallCommand installs the on-demand skill in any agent that supports
// the format, with no Go toolchain and no binary on PATH.
const SkillInstallCommand = "npx skills add " + Repository

// InstallCommand builds the binary itself, for agents that found the skill but
// not the executable it documents.
const InstallCommand = "go install github.com/" + Repository + "@latest"

// SkillMarkdown renders the installable Agent Skill from the same command table
// the CLI dispatches on, so the skill cannot drift from the tool. Live state is
// deliberately absent: a skill is static, the session hook shows what is current.
func SkillMarkdown() string {
	var b strings.Builder
	b.WriteString(`---
name: discord-axi
description: Read, search, post, edit and react to Discord messages from the shell, with unread and mention state; explains which commands risk a user account being disabled.
---

# discord-axi

`)
	b.WriteString(Description + ". Every command prints TOON on stdout, errors included.\n\n")
	b.WriteString(`## Accounts

` + "```sh" + `
discord-axi login --token "<token>" --as work --scope write
discord-axi auth list
discord-axi messages "<guild>/<channel>" --account work
` + "```" + `

Each account has a name, a kind (` + "`bot`" + ` or ` + "`user`" + `, detected at login) and a
scope. A ` + "`read`" + ` account cannot send, edit, delete, react or mark read: the request
is refused before it leaves the machine. **User tokens default to ` + "`read`" + `**; widen
one with ` + "`discord-axi auth scope <name> --write`" + ` only when the user asks.

Secrets live in the system keyring (` + "`--store file`" + ` writes a 0600 file instead); the
account index never holds the token. Which account runs is decided in this order:
` + "`--account <name>`" + `, then ` + AccountEnvVar + `, then a bare token in
` + TokenEnvVar + `, then the stored default.

If ` + "`discord-axi`" + ` is not on PATH, install it with ` + "`" + InstallCommand + "`" + `.

## Addressing a channel

A channel argument is a snowflake id, a ` + "`\"<guild>/<channel>\"`" + ` path, or a direct
message name. Names match case-insensitively; an ambiguous name fails with the list
of candidates instead of guessing.

## Commands

` + "```\n")

	rows := make([]*axi.Doc, 0)
	var writes, reads []string
	for _, command := range Commands() {
		effect := "read"
		if IsWrite(command.Name) {
			effect = "write"
			writes = append(writes, "`"+command.Name+"`")
		} else if !isAccountCommand(command.Name) {
			reads = append(reads, "`"+command.Name+"`")
		}
		rows = append(rows, axi.NewDoc().
			Set("name", command.Name).
			Set("effect", effect).
			Set("usage", command.Examples[0]).
			Set("description", command.Desc))
	}
	b.WriteString(axi.NewDoc().Set("commands", rows).Encode())
	b.WriteString("\n```\n\n")

	b.WriteString(`## Ban risk: read and write are not the same

With a **bot token** none of this applies — bots are what Discord expects, and
every command is fine.

With a **user token**, automating the account already breaks Discord's Terms of
Service, and the two halves of this CLI do not carry the same exposure:

- ` + strings.Join(reads, ", ") + ` only read. These look like the requests a
  browser makes anyway, and the upstream project's issue tracker has no report of
  an account being disabled while only reading.
- ` + strings.Join(writes, ", ") + ` change something. Every command marked
  ` + "`effect: write`" + ` in the table above acts as the account, and the one
  documented case of an account being disabled mid-use happened on the first
  message sent (` + "[discordo#813](https://github.com/ayn2op/discordo/issues/813)" + `).
  ` + "`read`" + ` counts as a write: it tells Discord the channel was seen.

Two further rules, from the same issue tracker: logging in with an email and
password through a third-party client is what disabled the accounts in
` + "[#691](https://github.com/ayn2op/discordo/issues/691)" + ` and
` + "[#816](https://github.com/ayn2op/discordo/issues/816)" + `, which is why this
CLI takes a token only — take one from an existing browser session; and a burst of
requests is the clearest self-bot signal, which is why the pacer exists — do not
disable it to go faster.

**Run ` + "`discord-axi whoami`" + ` before acting: it reports ` + "`kind: bot`" + ` or
` + "`kind: user`" + ` and the scope in force. On a user token, ask before running a
write command rather than assuming consent to the risk — and leave the account at
` + "`scope: read`" + ` unless the user wants writes.**

`)

	b.WriteString(`Run ` + "`discord-axi <command> --help`" + ` for the flags of one command.

## Notes

- Message lists are chronological, oldest first, and long bodies are truncated; ` + "`--full`" + ` prints them whole.
- ` + "`--limit`" + ` on ` + "`messages`" + ` caps at 100; page further back with ` + "`--before <id>`" + `.
- ` + "`search`" + `, ` + "`unread`" + ` and ` + "`read`" + ` need a user token: Discord does not give bots search or read state.
- ` + "`unread`" + ` and ` + "`read`" + ` need a gateway connection, so the first one starts a background daemon and every later call reuses it. Nothing to set up; it exits after 30 idle minutes. ` + "`DISCORD_AXI_NO_DAEMON=1`" + ` opts out, ` + "`discord-axi daemon status`" + ` and ` + "`daemon stop`" + ` inspect and end it.
- Requests are paced about a second apart across all processes, so a batch of commands takes a while on purpose. ` + "`DISCORD_AXI_MIN_INTERVAL_MS`" + ` tunes it.
- Exit codes: 0 for success and no-ops, 1 for errors, 2 for usage errors.
- Every write command repeats its own warning under ` + "`caution`" + ` in ` + "`--help`" + `.
`)
	return b.String()
}
