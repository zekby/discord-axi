package app

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/session"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/arikawa/v3/state/store/defaultstore"
	"github.com/ayn2op/arikawa/v3/utils/handler"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/ningen/v3"
	"github.com/zekby/discord-axi/internal/axi"
	clientgateway "github.com/zekby/discord-axi/internal/discordo/gateway"
	dhttp "github.com/zekby/discord-axi/internal/discordo/http"
)

// gatewayTimeout bounds the one-shot gateway connection that read state needs.
const gatewayTimeout = 20 * time.Second

// openState connects to the gateway exactly like Discordo does and blocks until
// Discord has sent the ready payload that carries read state.
func openState(token string) (*ningen.State, func(), error) {
	if IsBot(token) {
		return nil, nil, axi.Fail("FORBIDDEN", "Discord keeps read state for user accounts only",
			"Run `"+axi.Binary()+` messages "<guild>/<channel>"`+"` to read a channel with this bot token")
	}

	identifier := gateway.NewIdentifier(gateway.IdentifyCommand{
		Token:      token,
		Properties: dhttp.IdentifyProperties(),
	})
	discordSession := session.NewWithGateway(clientgateway.New(identifier), handler.New())
	discordSession.Client = NewClient(token)

	connected := ningen.FromState(state.NewFromSession(discordSession, defaultstore.New()))
	connected.OnRequest = append(connected.OnRequest, httputil.WithHeaders(dhttp.Headers()))

	ctx, cancel := context.WithTimeout(context.Background(), gatewayTimeout)
	if err := connected.Open(ctx); err != nil {
		cancel()
		if ctx.Err() != nil {
			return nil, nil, axi.Fail("GATEWAY_TIMEOUT", "Discord did not finish the initial sync in time",
				"Run the command again; large accounts occasionally need a second attempt")
		}
		return nil, nil, Translate(err)
	}
	return connected, func() {
		connected.Close()
		cancel()
	}, nil
}

// stateFor prefers the daemon's already-open session. Only a bare CLI run pays
// for a connection of its own, and it says so in the output.
func stateFor(token string) (*ningen.State, func(), bool, error) {
	if liveState != nil {
		return liveState, func() {}, true, nil
	}
	connected, closeState, err := openState(token)
	return connected, closeState, false, err
}

// oneShotHint nudges the agent toward the daemon, because repeatedly opening and
// closing a gateway connection is the least browser-like thing this CLI does.
func oneShotHint(persistent bool) []string {
	if persistent {
		return nil
	}
	return []string{
		"Run `" + axi.Binary() + " daemon run` to keep one connection open instead of reconnecting per command",
	}
}

type unreadEntry struct {
	channel  discord.Channel
	guild    string
	mentions int
	kind     string
}

func collectUnread(connected *ningen.State) []unreadEntry {
	opts := ningen.UnreadOpts{IncludeMutedCategories: true}
	var entries []unreadEntry

	appendIfUnread := func(channel discord.Channel, guildName string) {
		indication := connected.ChannelIsUnread(channel.ID, opts)
		if indication == ningen.ChannelRead {
			return
		}
		kind := "unread"
		if indication == ningen.ChannelMentioned {
			kind = "mentioned"
		}
		mentions := 0
		if readState := connected.ReadState.ReadState(channel.ID); readState != nil {
			mentions = readState.MentionCount
		}
		entries = append(entries, unreadEntry{
			channel:  channel,
			guild:    guildName,
			mentions: mentions,
			kind:     kind,
		})
	}

	guilds, _ := connected.Cabinet.Guilds()
	for _, guild := range guilds {
		channels, err := connected.Cabinet.Channels(guild.ID)
		if err != nil {
			continue
		}
		for _, channel := range readable(channels) {
			appendIfUnread(channel, guild.Name)
		}
	}
	if privates, err := connected.Cabinet.PrivateChannels(); err == nil {
		for _, channel := range privates {
			appendIfUnread(channel, "direct messages")
		}
	}

	// Mentions first, then the busiest conversations: that is the order an agent
	// wants to act in.
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			return entries[i].kind == "mentioned"
		}
		return entries[i].mentions > entries[j].mentions
	})
	return entries
}

func unreadCommand() *axi.Command {
	return &axi.Command{
		Name: "unread",
		Desc: "List channels with unread messages and mentions (user accounts only)",
		Flags: []axi.Flag{
			{Name: "--limit", Value: "n", Desc: "Max channels to print", Default: "50"},
			{Name: "--mentions", Desc: "Keep only channels that mention this account"},
		},
		Examples: []string{"discord-axi unread", "discord-axi unread --mentions"},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			if doc, served, err := forwardToDaemon("unread", inv.Raw); served {
				return doc, err
			}
			token, err := Token()
			if err != nil {
				return nil, err
			}
			limit, err := inv.Uint("--limit")
			if err != nil {
				return nil, err
			}
			connected, closeState, persistent, err := stateFor(token)
			if err != nil {
				return nil, err
			}
			defer closeState()

			entries := collectUnread(connected)
			if inv.Bool("--mentions") {
				kept := entries[:0:0]
				for _, entry := range entries {
					if entry.kind == "mentioned" {
						kept = append(kept, entry)
					}
				}
				entries = kept
			}
			if len(entries) == 0 {
				scope := "unread channels"
				if inv.Bool("--mentions") {
					scope = "channels mentioning this account"
				}
				empty := axi.NewDoc().Set("unread", "0 "+scope+" across every guild and direct message")
				if hint := oneShotHint(persistent); hint != nil {
					empty.Set("help", hint)
				}
				return empty, nil
			}

			shown := limited(entries, limit)
			rows := make([]*axi.Doc, 0, len(shown))
			for _, entry := range shown {
				rows = append(rows, axi.NewDoc().
					Set("id", entry.channel.ID.String()).
					Set("name", ChannelName(entry.channel)).
					Set("guild", entry.guild).
					Set("state", entry.kind).
					Set("mentions", entry.mentions))
			}
			help := []string{
				"Run `" + axi.Binary() + " messages <id>` to read one of these channels",
				"Run `" + axi.Binary() + " read <id>` to mark a channel read",
			}
			return axi.NewDoc().
				Set("count", countOf(len(shown), len(entries))).
				Set("mentions", strconv.Itoa(connected.ReadState.TotalMentionCount())+" total").
				Set("unread", rows).
				Set("help", append(help, oneShotHint(persistent)...)), nil
		},
	}
}

func markReadCommand() *axi.Command {
	return &axi.Command{
		Name:     "read",
		Desc:     "Mark a channel read up to its latest message (user accounts only)",
		Args:     []axi.Arg{{Name: "channel", Required: true}},
		Examples: []string{"discord-axi read 1234567890", `discord-axi read "My Server/general"`},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			if doc, served, err := forwardToDaemon("read", inv.Raw); served {
				return doc, err
			}
			token, err := Token()
			if err != nil {
				return nil, err
			}
			connected, closeState, _, err := stateFor(token)
			if err != nil {
				return nil, err
			}
			defer closeState()

			channel, err := ResolveChannel(NewClient(token), inv.Arg(0))
			if err != nil {
				return nil, err
			}
			if connected.ChannelIsUnread(channel.Channel.ID, ningen.UnreadOpts{IncludeMutedCategories: true}) == ningen.ChannelRead {
				return axi.NewDoc().Set("read", channel.Label+" was already read (no-op)"), nil
			}

			last := connected.LastMessage(channel.Channel.ID)
			if !last.IsValid() {
				last = channel.Channel.LastMessageID
			}
			connected.ReadState.MarkRead(channel.Channel.ID, last)
			// The ack is sent asynchronously; give it the moment it needs before
			// the connection closes.
			time.Sleep(500 * time.Millisecond)

			return axi.NewDoc().
				Set("read", channel.Label+" marked read up to message "+last.String()), nil
		},
	}
}
