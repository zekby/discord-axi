package app

import (
	"strings"

	"github.com/zekby/discord-axi/internal/axi"
)

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
	b.WriteString(`## Authentication

` + "```sh" + `
discord-axi login --token "<token>"
discord-axi login --email "<email>" --password "<password>" --code <2fa-code>
` + "```" + `

The token is kept in the system keyring. ` + TokenEnvVar + ` overrides it for one shell.
Bot tokens and user tokens both work; the CLI detects which prefix Discord expects.

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
		} else if command.Name != "login" && command.Name != "logout" && command.Name != "setup" && command.Name != "daemon" {
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

Two further rules, from the same issue tracker: password login through a
third-party client is what disabled accounts in
` + "[#691](https://github.com/ayn2op/discordo/issues/691)" + ` and
` + "[#816](https://github.com/ayn2op/discordo/issues/816)" + `, so prefer a token
taken from an existing browser session; and a burst of requests is the clearest
self-bot signal, which is why the pacer exists — do not disable it to go faster.

**If the user has not said which token is in use, run ` + "`discord-axi whoami`" + `
first: it reports ` + "`kind: bot`" + ` or ` + "`kind: user`" + `. On a user token,
ask before running a write command rather than assuming consent to the risk.**

`)

	b.WriteString(`Run ` + "`discord-axi <command> --help`" + ` for the flags of one command.

## Notes

- Message lists are chronological, oldest first, and long bodies are truncated; ` + "`--full`" + ` prints them whole.
- ` + "`--limit`" + ` on ` + "`messages`" + ` caps at 100; page further back with ` + "`--before <id>`" + `.
- ` + "`search`" + `, ` + "`unread`" + ` and ` + "`read`" + ` need a user token: Discord does not give bots search or read state.
- ` + "`unread`" + ` and ` + "`read`" + ` open a gateway connection. Start ` + "`discord-axi daemon run`" + ` once and they reuse a single long-lived session instead.
- Requests are paced about a second apart across all processes, so a batch of commands takes a while on purpose. ` + "`DISCORD_AXI_MIN_INTERVAL_MS`" + ` tunes it.
- Exit codes: 0 for success and no-ops, 1 for errors, 2 for usage errors.
- Every write command repeats its own warning under ` + "`caution`" + ` in ` + "`--help`" + `.
`)
	return b.String()
}
