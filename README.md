# discord-axi

Discord for coding agents: the same client stack as
[Discordo](https://github.com/ayn2op/discordo), reshaped from a TUI into an
[AXI](https://skills.sh/kunchenguid/axi) command line tool.

Where Discordo draws guilds, channels and messages on a terminal screen,
`discord-axi` prints them as TOON on stdout — the format agents read for about
40% fewer tokens than JSON — and takes every input as a flag, so nothing ever
blocks on a prompt.

> [!IMPORTANT]
> Automating a user account ("self-bot") is against Discord's Terms of Service.
> Use a bot token unless you accept that risk. Search, unread state and marking
> channels read only exist for user accounts, because Discord does not offer
> them to bots. A user account is stored read-only unless you widen it yourself.
>
> Reads and writes are not equally exposed. `whoami`, `guilds`, `channels`,
> `dms`, `messages`, `search` and `unread` only read; `send`, `edit`, `delete`,
> `react` and `read` act as the account, are marked `effect: write` in `SKILL.md`
> and repeat the warning under `caution` in their own `--help`. In upstream's
> issue tracker the disablings happened on email-and-password login
> ([#691](https://github.com/ayn2op/discordo/issues/691),
> [#816](https://github.com/ayn2op/discordo/issues/816)) — which is why this CLI
> takes a token only — and on the first message
> sent ([#813](https://github.com/ayn2op/discordo/issues/813)) — never while only
> reading. With a bot token none of this applies.

## What it borrows from Discordo

`internal/discordo/` is copied from the upstream project, unmodified apart from
import paths:

| Package    | What it does                                                                 |
| ---------- | ---------------------------------------------------------------------------- |
| `http`     | API client with the browser user agent, super properties and identify payload |
| `tls`      | Chrome TLS profile, so Discord sees an ordinary browser handshake             |
| `gateway`  | Websocket dialer over the same TLS client                                     |
| `keyring`  | Token storage in the OS keyring, one entry per account                        |

On top of that it uses the same libraries: `arikawa` for the REST API and the
gateway, `ningen` for read state — so `unread` reproduces Discordo's unread and
mention indicators exactly.

Build without the TLS profile with `-tags no_spoof_tls_fingerprint`, exactly as
upstream does.

## Install

```sh
go build -o discord-axi .
mv discord-axi ~/.local/bin/
```

## Use

```sh
discord-axi login --token "<token>" --as work   # store an account
discord-axi                                     # account and guilds
discord-axi channels "My Server"
discord-axi messages "My Server/general" --limit 50
discord-axi send "My Server/general" --content "deploy finished"
discord-axi unread --mentions
```

A channel argument is a snowflake id, a `"<guild>/<channel>"` path, or a direct
message name. An ambiguous name is an error listing the candidates — never a
guess.

Every subcommand answers `--help` with its own flags, defaults and examples.

## Accounts and scopes

Several accounts can be stored side by side. Each carries a kind — `bot` or
`user`, detected at login — and a scope that decides what it may do:

```sh
discord-axi login --token "<bot-token>" --as ci --scope write
discord-axi login --token "<user-token>" --as personal   # read-only by default
discord-axi auth list
discord-axi auth use personal            # change the default account
discord-axi auth scope personal --write  # allow writes, deliberately
discord-axi messages "My Server/general" --account ci
```

A `read` account cannot send, edit, delete, react or mark read: the request is
refused inside the HTTP client, before it leaves the machine, so a command added
later cannot forget the check. `--account` works on every command.

Tokens live in the OS keyring — `--store file` writes a `0600` file instead, for
machines without one — and the account index never holds a secret.
`DISCORD_AXI_ACCOUNT` picks a stored account for one shell; `DISCORD_AXI_TOKEN`
overrides everything with a bare token, read-only if `DISCORD_AXI_SCOPE=read`.

## Two ways to give an agent this tool

Install whichever fits; you do not need both.

1. **Session hook (recommended).** `discord-axi setup hooks` registers the home
   view as session-start context for Claude Code, Codex and OpenCode, so every
   session opens with the account and its guilds already visible. Re-running it
   repairs the path after a reinstall and is otherwise a silent no-op.
2. **Skill.** `SKILL.md` in this repository is an on-demand
   [Agent Skill](https://agentskills.io) that costs nothing per session:

   ```sh
   npx skills add <owner>/discord-axi
   ```

   It is generated from the command table, and `go test ./...` fails if it drifts.
   Regenerate with `discord-axi setup skill --path SKILL.md`.

## Looking like a client, not a script

Three things make a third-party client stand out. None of this makes using a
user token allowed — it only removes the noise a real browser would never make.

**One connection, not one per command.** Read state needs the gateway, and
connecting then disconnecting for every `unread` is nothing like a browser that
stays online for hours. So the first `unread` or `read` starts a daemon by
itself, in the background, and every later call is answered over its socket
without opening anything:

```sh
discord-axi unread           # starts the daemon if none is up, then uses it
discord-axi daemon status
discord-axi daemon stop
```

Nothing to remember and nothing to background by hand. The daemon shuts itself
down after 30 minutes without a request, so it does not linger:

```sh
discord-axi daemon start --idle 120   # start it yourself, two hour idle timeout
discord-axi daemon start --idle 0     # stay up until stopped
discord-axi daemon run                # hold it in the foreground instead
DISCORD_AXI_NO_DAEMON=1 discord-axi unread   # never start one
```

With auto-start refused or failing, the commands still work; they open a
connection of their own and say so in their output. Its log is at
`~/.local/state/discord-axi/daemon.log`.

**Paced requests.** Every REST call takes a slot from a lock file shared by all
`discord-axi` processes, so an agent firing twenty commands at once still sends
them roughly a second apart, with jitter. Tune or disable it:

```sh
DISCORD_AXI_MIN_INTERVAL_MS=2000 discord-axi messages "My Server/general"
DISCORD_AXI_MIN_INTERVAL_MS=0    discord-axi guilds      # no pacing
```

The default of 1.2s means a command that resolves a guild and a channel by name
takes a few seconds. That is the point.

**A current build number.** The identify payload carries the web client's build
number, and a value frozen at compile time drifts within weeks — Discordo's
pinned `584177` is thousands of builds behind today's. It is re-read from
Discord's own login page at most once a day and cached in
`~/.local/state/discord-axi/`, falling back to the compiled value if that fails.

## Conventions

- **stdout** carries data, errors and suggestions; nothing else is printed there.
- **Exit codes**: `0` success, including no-ops such as deleting an already
  deleted message; `1` runtime error; `2` usage error, including an unknown flag.
- Message lists are chronological and truncated at 700 characters, with the
  total size and a `--full` escape hatch.
- Lists carry the total count, so an agent never paginates to find out how many
  there are.

## Development

```sh
go test ./...
go vet ./...
```

## License

GPL-3.0, inherited from Discordo. See `LICENSE` and `NOTICE`.
