---
name: discord-axi
description: Read, search, post, edit and react to Discord messages from the shell, with unread and mention state; explains which commands risk a user account being disabled.
---

# discord-axi

Read and write Discord from the shell, in agent-readable TOON. Every command prints TOON on stdout, errors included.

## Accounts

```sh
discord-axi login --token "<token>" --as work --scope write
discord-axi auth list
discord-axi messages "<guild>/<channel>" --account work
```

Each account has a name, a kind (`bot` or `user`, detected at login) and a
scope. A `read` account cannot send, edit, delete, react or mark read: the request
is refused before it leaves the machine. **User tokens default to `read`**; widen
one with `discord-axi auth scope <name> --write` only when the user asks.

Secrets live in the system keyring (`--store file` writes a 0600 file instead); the
account index never holds the token. Which account runs is decided in this order:
`--account <name>`, then DISCORD_AXI_ACCOUNT, then a bare token in
DISCORD_AXI_TOKEN, then the stored default.

A token in DISCORD_AXI_TOKEN follows the same rule as a login: it may only read
unless DISCORD_AXI_SCOPE=write says otherwise.

If `discord-axi` is not on PATH, install it with `go install github.com/zekby/discord-axi@latest`.

## Addressing a channel

A channel argument is a snowflake id, a `"<guild>/<channel>"` path, or a direct
message name. Names match case-insensitively; an ambiguous name fails with the list
of candidates instead of guessing.

## Commands

```
commands[17]{name,effect,usage,description}:
  login,read,"discord-axi login --token \"<bot-token>\" --as work --scope write",Verify a token and store it as a named account
  logout,read,discord-axi logout,Forget a stored account and its secret
  whoami,read,discord-axi whoami,Show which account a command would run as
  auth,read,discord-axi auth list,"List stored accounts, pick the default, or change what one may do"
  guilds,read,discord-axi guilds,List the guilds this account can reach
  channels,read,"discord-axi channels \"My Server\"",List the channels of a guild
  dms,read,discord-axi dms,"List direct message channels, most recent first"
  messages,read,"discord-axi messages \"My Server/general\"","Read recent messages of a channel, oldest first"
  send,write,"discord-axi send \"My Server/general\" --content \"deploy finished\"",Post a message to a channel
  edit,write,"discord-axi edit \"My Server/general\" 9876543210 --content \"fixed typo\"",Edit a message this account sent
  delete,write,"discord-axi delete \"My Server/general\" 9876543210",Delete a message
  react,write,"discord-axi react \"My Server/general\" 9876543210 --emoji \"✅\"",Add or remove this account's reaction on a message
  search,read,"discord-axi search \"My Server\" --content \"deploy failed\"",Search a guild's message history (user accounts only)
  unread,read,discord-axi unread,List channels with unread messages and mentions (user accounts only)
  read,write,discord-axi read 1234567890,Mark a channel read up to its latest message (user accounts only)
  daemon,read,discord-axi daemon start,Hold one long-lived gateway connection and serve read commands from it
  setup,read,discord-axi setup hooks,"Install the session-start hooks, or regenerate this repository's SKILL.md"
```

## Ban risk: read and write are not the same

With a **bot token** none of this applies — bots are what Discord expects, and
every command is fine.

With a **user token**, automating the account already breaks Discord's Terms of
Service, and the two halves of this CLI do not carry the same exposure:

- `guilds`, `channels`, `dms`, `messages`, `search`, `unread` only read. These look like the requests a
  browser makes anyway, and the upstream project's issue tracker has no report of
  an account being disabled while only reading.
- `send`, `edit`, `delete`, `react`, `read` change something. Every command marked
  `effect: write` in the table above acts as the account, and the one
  documented case of an account being disabled mid-use happened on the first
  message sent ([discordo#813](https://github.com/ayn2op/discordo/issues/813)).
  `read` counts as a write: it tells Discord the channel was seen.

Two further rules, from the same issue tracker: logging in with an email and
password through a third-party client is what disabled the accounts in
[#691](https://github.com/ayn2op/discordo/issues/691) and
[#816](https://github.com/ayn2op/discordo/issues/816), which is why this
CLI takes a token only — take one from an existing browser session; and a burst of
requests is the clearest self-bot signal, which is why the pacer exists — do not
disable it to go faster.

**Run `discord-axi whoami` before acting: it reports `kind: bot` or
`kind: user` and the scope in force. On a user token, ask before running a
write command rather than assuming consent to the risk — and leave the account at
`scope: read` unless the user wants writes.**

Run `discord-axi <command> --help` for the flags of one command.

## Notes

- Message lists are chronological, oldest first, and long bodies are truncated; `--full` prints them whole.
- `--limit` on `messages` caps at 100; page further back with `--before <id>`.
- `search`, `unread` and `read` need a user token: Discord does not give bots search or read state.
- `unread` and `read` need a gateway connection, so the first one starts a background daemon and every later call reuses it. Nothing to set up; it exits after 30 idle minutes. `DISCORD_AXI_NO_DAEMON=1` opts out, `discord-axi daemon status` and `daemon stop` inspect and end it.
- Requests are paced about a second apart across all processes, so a batch of commands takes a while on purpose. `DISCORD_AXI_MIN_INTERVAL_MS` tunes it.
- Exit codes: 0 for success and no-ops, 1 for errors, 2 for usage errors.
- Every write command repeats its own warning under `caution` in `--help`.
