---
name: discord-axi
description: Read, search, post, edit and react to Discord messages from the shell, with unread and mention state; explains which commands risk a user account being disabled.
---

# discord-axi

Read and write Discord from the shell, in agent-readable TOON. Every command prints TOON on stdout, errors included.

## Authentication

```sh
discord-axi login --token "<token>"
discord-axi login --email "<email>" --password "<password>" --code <2fa-code>
```

The token is kept in the system keyring. DISCORD_AXI_TOKEN overrides it for one shell.
Bot tokens and user tokens both work; the CLI detects which prefix Discord expects.

## Addressing a channel

A channel argument is a snowflake id, a `"<guild>/<channel>"` path, or a direct
message name. Names match case-insensitively; an ambiguous name fails with the list
of candidates instead of guessing.

## Commands

```
commands[16]{name,effect,usage,description}:
  login,read,"discord-axi login --token \"<token>\"","Store a Discord token in the system keyring, or exchange email and password for one"
  logout,read,discord-axi logout,Remove the stored token from the system keyring
  whoami,read,discord-axi whoami,Show the authenticated account
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
  daemon,read,discord-axi daemon run,Hold one long-lived gateway connection and serve read commands from it
  setup,read,discord-axi setup hooks,"Install the session-start hooks, or write the installable skill file"
```

## Ban risk: read and write are not the same

With a **bot token** none of this applies — bots are what Discord expects, and
every command is fine.

With a **user token**, automating the account already breaks Discord's Terms of
Service, and the two halves of this CLI do not carry the same exposure:

- `whoami`, `guilds`, `channels`, `dms`, `messages`, `search`, `unread` only read. These look like the requests a
  browser makes anyway, and the upstream project's issue tracker has no report of
  an account being disabled while only reading.
- `send`, `edit`, `delete`, `react`, `read` change something. Every command marked
  `effect: write` in the table above acts as the account, and the one
  documented case of an account being disabled mid-use happened on the first
  message sent ([discordo#813](https://github.com/ayn2op/discordo/issues/813)).
  `read` counts as a write: it tells Discord the channel was seen.

Two further rules, from the same issue tracker: password login through a
third-party client is what disabled accounts in
[#691](https://github.com/ayn2op/discordo/issues/691) and
[#816](https://github.com/ayn2op/discordo/issues/816), so prefer a token
taken from an existing browser session; and a burst of requests is the clearest
self-bot signal, which is why the pacer exists — do not disable it to go faster.

**If the user has not said which token is in use, run `discord-axi whoami`
first: it reports `kind: bot` or `kind: user`. On a user token,
ask before running a write command rather than assuming consent to the risk.**

Run `discord-axi <command> --help` for the flags of one command.

## Notes

- Message lists are chronological, oldest first, and long bodies are truncated; `--full` prints them whole.
- `--limit` on `messages` caps at 100; page further back with `--before <id>`.
- `search`, `unread` and `read` need a user token: Discord does not give bots search or read state.
- `unread` and `read` open a gateway connection. Start `discord-axi daemon run` once and they reuse a single long-lived session instead.
- Requests are paced about a second apart across all processes, so a batch of commands takes a while on purpose. `DISCORD_AXI_MIN_INTERVAL_MS` tunes it.
- Exit codes: 0 for success and no-ops, 1 for errors, 2 for usage errors.
- Every write command repeats its own warning under `caution` in `--help`.
