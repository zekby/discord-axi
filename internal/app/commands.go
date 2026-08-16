package app

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/sendpart"
	"github.com/zekby/discord-axi/internal/axi"
	"github.com/zekby/discord-axi/internal/discordo/keyring"
)

const (
	// truncateAt keeps a detail view useful without paying for whole essays.
	truncateAt = 700
	// homeGuilds bounds the ambient session-start view.
	homeGuilds       = 10
	messageFetchCap  = 100
	defaultMsgLimit  = "30"
	defaultListLimit = "200"
)

func timestamp(when discord.Timestamp) string {
	return when.Time().UTC().Format("2006-01-02T15:04Z")
}

func truncate(content string, full bool) (string, bool) {
	if full || len(content) <= truncateAt {
		return content, false
	}
	return content[:truncateAt] + "... (truncated, " + strconv.Itoa(len(content)) + " chars total)", true
}

func countOf(shown, total int) string {
	return strconv.Itoa(shown) + " of " + strconv.Itoa(total) + " total"
}

func limited[T any](items []T, limit uint) []T {
	if limit == 0 || uint(len(items)) <= limit {
		return items
	}
	return items[:limit]
}

// fieldSet maps the names accepted by --fields to their extractors.
type fieldSet[T any] map[string]func(T) any

func (f fieldSet[T]) names() string {
	names := make([]string, 0, len(f))
	for name := range f {
		names = append(names, name)
	}
	sortStrings(names)
	return strings.Join(names, ", ")
}

func (f fieldSet[T]) selected(inv *axi.Invocation) ([]string, error) {
	raw := inv.String("--fields")
	if raw == "" {
		return nil, nil
	}
	var chosen []string
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := f[name]; !ok {
			return nil, axi.Usage(`unknown field "`+name+"\" for `"+inv.Command.Name+"`",
				"available fields: "+f.names())
		}
		chosen = append(chosen, name)
	}
	return chosen, nil
}

func (f fieldSet[T]) apply(row *axi.Doc, item T, chosen []string) *axi.Doc {
	for _, name := range chosen {
		row.Set(name, f[name](item))
	}
	return row
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// WriteCaution is attached to every command that changes something on Discord.
// An agent reading a help block or the skill must be able to tell a read from a
// write before it runs one, because the two carry very different consequences
// on a user token.
const WriteCaution = "Writes to Discord as the logged-in account. With a user token that breaks Discord's Terms of Service and risks the account being disabled; bot tokens are unaffected"

// IsWrite reports whether a command mutates Discord state.
func IsWrite(name string) bool {
	switch name {
	case "send", "edit", "delete", "react", "read":
		return true
	default:
		return false
	}
}

func fieldsFlag(available string) axi.Flag {
	return axi.Flag{Name: "--fields", Value: "a,b", Desc: "Extra fields: " + available}
}

func jumpURL(guildID discord.GuildID, channelID discord.ChannelID, messageID discord.MessageID) string {
	scope := "@me"
	if guildID.IsValid() {
		scope = guildID.String()
	}
	return "https://discord.com/channels/" + scope + "/" + channelID.String() + "/" + messageID.String()
}

var guildFields = fieldSet[discord.Guild]{
	"owner":       func(g discord.Guild) any { return g.OwnerID.String() },
	"description": func(g discord.Guild) any { return g.Description },
	"members":     func(g discord.Guild) any { return int(g.ApproximateMembers) },
}

var channelFields = fieldSet[discord.Channel]{
	"topic":    func(c discord.Channel) any { return c.Topic },
	"nsfw":     func(c discord.Channel) any { return c.NSFW },
	"position": func(c discord.Channel) any { return int(c.Position) },
	"parent":   func(c discord.Channel) any { return c.ParentID.String() },
}

var messageFields = fieldSet[discord.Message]{
	"reactions":   func(m discord.Message) any { return reactionSummary(m) },
	"attachments": func(m discord.Message) any { return len(m.Attachments) },
	"replyTo":     func(m discord.Message) any { return referencedID(m) },
	"edited":      func(m discord.Message) any { return m.EditedTimestamp.IsValid() },
	"pinned":      func(m discord.Message) any { return m.Pinned },
	"url":         func(m discord.Message) any { return jumpURL(m.GuildID, m.ChannelID, m.ID) },
}

func reactionSummary(message discord.Message) string {
	parts := make([]string, 0, len(message.Reactions))
	for _, reaction := range message.Reactions {
		parts = append(parts, reaction.Emoji.Name+":"+strconv.Itoa(reaction.Count))
	}
	return strings.Join(parts, " ")
}

func referencedID(message discord.Message) string {
	if message.Reference == nil || !message.Reference.MessageID.IsValid() {
		return ""
	}
	return message.Reference.MessageID.String()
}

// Commands is the full surface of the CLI, in the order the top-level help
// lists them.
func Commands() []*axi.Command {
	commands := []*axi.Command{
		loginCommand(),
		logoutCommand(),
		whoamiCommand(),
		guildsCommand(),
		channelsCommand(),
		dmsCommand(),
		messagesCommand(),
		sendCommand(),
		editCommand(),
		deleteCommand(),
		reactCommand(),
		searchCommand(),
		unreadCommand(),
		markReadCommand(),
		daemonCommand(),
		setupCommand(),
	}
	for _, command := range commands {
		if IsWrite(command.Name) {
			command.Caution = WriteCaution
		}
	}
	return commands
}

func loginCommand() *axi.Command {
	return &axi.Command{
		Name: "login",
		Desc: "Store a Discord token in the system keyring, or exchange email and password for one",
		Flags: []axi.Flag{
			{Name: "--token", Value: "token", Desc: "Existing user or bot token"},
			{Name: "--email", Value: "email", Desc: "Email or E.164 phone number, for password login"},
			{Name: "--password", Value: "password", Desc: "Account password, for password login"},
			{Name: "--code", Value: "code", Desc: "Two-factor code, when the account requires one"},
		},
		Examples: []string{
			`discord-axi login --token "<token>"`,
			`discord-axi login --email "me@example.com" --password "<password>" --code 123456`,
		},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			token := inv.String("--token")
			email := inv.String("--email")
			password := inv.String("--password")

			switch {
			case token != "" && (email != "" || password != ""):
				return nil, axi.Usage("--token cannot be combined with --email or --password",
					"Run `"+axi.Binary()+` login --token "<token>"`+"`",
					"Run `"+axi.Binary()+` login --email "<email>" --password "<password>"`+"`")
			case token == "" && (email == "" || password == ""):
				return nil, axi.Usage("--token, or both --email and --password, are required for `login`",
					"Run `"+axi.Binary()+` login --token "<token>"`+"`",
					"Run `"+axi.Binary()+` login --email "<email>" --password "<password>"`+"`")
			}

			if token == "" {
				resolved, err := passwordLogin(email, password, inv.String("--code"))
				if err != nil {
					return nil, err
				}
				token = resolved
			}

			authorized, me, err := verify(token)
			if err != nil {
				return nil, err
			}
			if err := keyring.SetToken(authorized); err != nil {
				return nil, axi.Fail("KEYRING_ERROR", "could not store the token in the system keyring",
					"Set "+TokenEnvVar+" in the environment instead")
			}
			return axi.NewDoc().
				Set("login", "authenticated as "+me.DisplayOrUsername()+" ("+TokenKind(authorized)+")").
				Set("id", me.ID.String()).
				Set("stored", "system keyring, service "+axi.Binary()).
				Set("help", []string{"Run `" + axi.Binary() + "` to see the account and its guilds"}), nil
		},
	}
}

// verify decides how the token must be presented. A bot token is only valid
// behind the "Bot " prefix, a user token only without it.
func verify(token string) (string, *discord.User, error) {
	candidates := []string{token}
	if !IsBot(token) {
		candidates = append(candidates, BotPrefix+token)
	}
	for _, candidate := range candidates {
		me, err := NewClient(candidate).Me()
		if err == nil {
			return candidate, me, nil
		}
		if !isUnauthorized(err) {
			return "", nil, Translate(err)
		}
	}
	return "", nil, axi.Fail("NOT_AUTHENTICATED", "token rejected by Discord",
		"Check the token was copied in full and has not been reset")
}

func passwordLogin(email, password, code string) (string, error) {
	client := NewClient("")
	response, err := client.Login(email, password)
	if err != nil {
		return "", Translate(err)
	}
	if response.Token != "" {
		return response.Token, nil
	}
	if !response.MFA {
		return "", axi.Fail("LOGIN_FAILED", "Discord did not return a token for those credentials",
			"Log in once in the Discord client to clear any pending verification, then retry")
	}
	if code == "" {
		return "", axi.Usage("this account requires a two-factor code",
			"Run `"+axi.Binary()+` login --email "<email>" --password "<password>" --code <code>`+"`")
	}
	verified, err := client.TOTP(code, response.Ticket, response.LoginInstanceID)
	if err != nil {
		return "", Translate(err)
	}
	return verified.Token, nil
}

func logoutCommand() *axi.Command {
	return &axi.Command{
		Name:     "logout",
		Desc:     "Remove the stored token from the system keyring",
		Examples: []string{"discord-axi logout"},
		Run: func(*axi.Invocation) (*axi.Doc, error) {
			if os.Getenv(TokenEnvVar) != "" {
				return axi.NewDoc().
					Set("logout", TokenEnvVar+" is set in the environment and takes precedence (no-op)").
					Set("help", []string{"Unset " + TokenEnvVar + " in the shell to stop using it"}), nil
			}
			if _, err := keyring.GetToken(); err != nil {
				return axi.NewDoc().Set("logout", "no stored token (no-op)"), nil
			}
			if err := keyring.DeleteToken(); err != nil {
				return nil, axi.Fail("KEYRING_ERROR", "could not remove the token from the system keyring")
			}
			return axi.NewDoc().Set("logout", "stored token removed"), nil
		},
	}
}

func whoamiCommand() *axi.Command {
	return &axi.Command{
		Name:     "whoami",
		Desc:     "Show the authenticated account",
		Examples: []string{"discord-axi whoami"},
		Run: func(*axi.Invocation) (*axi.Doc, error) {
			token, err := Token()
			if err != nil {
				return nil, err
			}
			me, err := NewClient(token).Me()
			if err != nil {
				return nil, Translate(err)
			}
			source := "system keyring"
			if os.Getenv(TokenEnvVar) != "" {
				source = TokenEnvVar + " environment variable"
			}
			return axi.NewDoc().
				Set("account", me.DisplayOrUsername()).
				Set("id", me.ID.String()).
				Set("username", me.Username).
				Set("kind", TokenKind(token)).
				Set("source", source), nil
		},
	}
}

func guildsCommand() *axi.Command {
	return &axi.Command{
		Name: "guilds",
		Desc: "List the guilds this account can reach",
		Flags: []axi.Flag{
			{Name: "--limit", Value: "n", Desc: "Max guilds to print", Default: defaultListLimit},
			fieldsFlag(guildFields.names()),
		},
		Examples: []string{"discord-axi guilds", "discord-axi guilds --fields members"},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			client, err := Client()
			if err != nil {
				return nil, err
			}
			limit, err := inv.Uint("--limit")
			if err != nil {
				return nil, err
			}
			fields, err := guildFields.selected(inv)
			if err != nil {
				return nil, err
			}

			guilds, err := client.Guilds(0)
			if err != nil {
				return nil, Translate(err)
			}
			if len(guilds) == 0 {
				return axi.NewDoc().
					Set("guilds", "0 guilds found for this account").
					Set("help", []string{"Run `" + axi.Binary() + " dms` to list direct messages instead"}), nil
			}

			shown := limited(guilds, limit)
			rows := make([]*axi.Doc, 0, len(shown))
			for _, guild := range shown {
				rows = append(rows, guildFields.apply(
					axi.NewDoc().Set("id", guild.ID.String()).Set("name", guild.Name),
					guild, fields))
			}
			return axi.NewDoc().
				Set("count", countOf(len(shown), len(guilds))).
				Set("guilds", rows).
				Set("help", []string{
					"Run `" + axi.Binary() + ` channels "<guild>"` + "` to list its channels",
					"Run `" + axi.Binary() + ` messages "<guild>/<channel>"` + "` to read a channel",
				}), nil
		},
	}
}

func channelsCommand() *axi.Command {
	return &axi.Command{
		Name: "channels",
		Desc: "List the channels of a guild",
		Args: []axi.Arg{{Name: "guild", Required: true}},
		Flags: []axi.Flag{
			{Name: "--limit", Value: "n", Desc: "Max channels to print", Default: defaultListLimit},
			{Name: "--type", Value: "text|forum|...", Desc: "Keep only channels of this type"},
			{Name: "--all", Desc: "Include voice, stage and category channels"},
			fieldsFlag(channelFields.names()),
		},
		Examples: []string{`discord-axi channels "My Server"`, "discord-axi channels 1234567890 --type text"},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			client, err := Client()
			if err != nil {
				return nil, err
			}
			limit, err := inv.Uint("--limit")
			if err != nil {
				return nil, err
			}
			fields, err := channelFields.selected(inv)
			if err != nil {
				return nil, err
			}
			guild, err := ResolveGuild(client, inv.Arg(0))
			if err != nil {
				return nil, err
			}
			channels, err := client.Channels(guild.ID)
			if err != nil {
				return nil, Translate(err)
			}

			visible := channels
			if !inv.Bool("--all") {
				visible = readable(channels)
			}
			if wanted := inv.String("--type"); wanted != "" {
				kept := visible[:0:0]
				for _, channel := range visible {
					if ChannelKind(channel) == wanted {
						kept = append(kept, channel)
					}
				}
				visible = kept
			}
			SortGuildChannels(visible)

			if len(visible) == 0 {
				kind := ""
				if wanted := inv.String("--type"); wanted != "" {
					kind = wanted + " "
				}
				return axi.NewDoc().
					Set("channels", "0 "+kind+"channels found in "+guild.Name).
					Set("help", []string{
						"Run `" + axi.Binary() + ` channels "` + guild.Name + `" --all` + "` to include voice and categories",
					}), nil
			}

			shown := limited(visible, limit)
			rows := make([]*axi.Doc, 0, len(shown))
			for _, channel := range shown {
				rows = append(rows, channelFields.apply(
					axi.NewDoc().
						Set("id", channel.ID.String()).
						Set("name", ChannelName(channel)).
						Set("type", ChannelKind(channel)),
					channel, fields))
			}
			return axi.NewDoc().
				Set("guild", guild.Name).
				Set("count", countOf(len(shown), len(visible))).
				Set("channels", rows).
				Set("help", []string{
					"Run `" + axi.Binary() + ` messages "` + guild.Name + `/<channel>"` + "` to read a channel",
					"Run `" + axi.Binary() + ` send "` + guild.Name + `/<channel>" --content "<text>"` + "` to post",
				}), nil
		},
	}
}

func dmsCommand() *axi.Command {
	return &axi.Command{
		Name: "dms",
		Desc: "List direct message channels, most recent first",
		Flags: []axi.Flag{
			{Name: "--limit", Value: "n", Desc: "Max conversations to print", Default: "50"},
		},
		Examples: []string{"discord-axi dms"},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			token, err := Token()
			if err != nil {
				return nil, err
			}
			limit, err := inv.Uint("--limit")
			if err != nil {
				return nil, err
			}
			channels, err := NewClient(token).PrivateChannels()
			if err != nil {
				return nil, Translate(err)
			}
			if len(channels) == 0 {
				empty := "0 direct message channels found"
				if IsBot(token) {
					empty = "0 direct message channels (bot accounts only see a DM after a user opens one)"
				}
				return axi.NewDoc().
					Set("dms", empty).
					Set("help", []string{"Run `" + axi.Binary() + " guilds` to list guilds instead"}), nil
			}

			SortPrivateChannels(channels)
			shown := limited(channels, limit)
			rows := make([]*axi.Doc, 0, len(shown))
			for _, channel := range shown {
				rows = append(rows, axi.NewDoc().
					Set("id", channel.ID.String()).
					Set("name", ChannelName(channel)).
					Set("type", ChannelKind(channel)))
			}
			return axi.NewDoc().
				Set("count", countOf(len(shown), len(channels))).
				Set("dms", rows).
				Set("help", []string{"Run `" + axi.Binary() + " messages <id>` to read a conversation"}), nil
		},
	}
}

func messagesCommand() *axi.Command {
	return &axi.Command{
		Name: "messages",
		Desc: "Read recent messages of a channel, oldest first",
		Args: []axi.Arg{{Name: "channel", Required: true}},
		Flags: []axi.Flag{
			{Name: "--limit", Value: "n", Desc: "Messages to fetch, 1-100", Default: defaultMsgLimit},
			{Name: "--before", Value: "message-id", Desc: "Fetch messages older than this id"},
			{Name: "--full", Desc: "Print complete bodies instead of truncating"},
			fieldsFlag(messageFields.names()),
		},
		Examples: []string{
			`discord-axi messages "My Server/general"`,
			"discord-axi messages 1234567890 --limit 50 --fields reactions,url",
		},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			client, err := Client()
			if err != nil {
				return nil, err
			}
			limit, err := inv.Uint("--limit")
			if err != nil {
				return nil, err
			}
			if limit > messageFetchCap {
				return nil, axi.Usage("--limit must be "+strconv.Itoa(messageFetchCap)+" or less",
					"Run `"+axi.Binary()+` messages "`+inv.Arg(0)+`" --limit 100`+"`",
					"Run `"+axi.Binary()+` messages "`+inv.Arg(0)+`" --before <oldest-id>`+"` to page further back")
			}
			fields, err := messageFields.selected(inv)
			if err != nil {
				return nil, err
			}
			channel, err := ResolveChannel(client, inv.Arg(0))
			if err != nil {
				return nil, err
			}

			var before discord.MessageID
			if raw := inv.String("--before"); raw != "" {
				id, parseErr := discord.ParseSnowflake(raw)
				if parseErr != nil {
					return nil, axi.Usage(`--before must be a message id, got "`+raw+`"`,
						"Run `"+axi.Binary()+` messages "`+inv.Arg(0)+`"`+"` and reuse an id from the output")
				}
				before = discord.MessageID(id)
			}

			messages, err := client.MessagesBefore(channel.Channel.ID, before, limit)
			if err != nil {
				return nil, Translate(err)
			}
			if len(messages) == 0 {
				return axi.NewDoc().
					Set("messages", "0 messages found in "+channel.Label).
					Set("help", []string{
						"Run `" + axi.Binary() + ` send "` + inv.Arg(0) + `" --content "<text>"` + "` to post the first one",
					}), nil
			}

			full := inv.Bool("--full")
			truncated := false
			rows := make([]*axi.Doc, 0, len(messages))
			for i := len(messages) - 1; i >= 0; i-- {
				message := messages[i]
				body, cut := truncate(message.Content, full)
				truncated = truncated || cut
				rows = append(rows, messageFields.apply(
					axi.NewDoc().
						Set("id", message.ID.String()).
						Set("author", message.Author.DisplayOrUsername()).
						Set("time", timestamp(message.Timestamp)).
						Set("content", body),
					message, fields))
			}

			help := []string{"Run `" + axi.Binary() + ` send "` + inv.Arg(0) + `" --content "<text>"` + "` to post a reply"}
			if truncated {
				help = append([]string{
					"Run `" + axi.Binary() + ` messages "` + inv.Arg(0) + `" --full` + "` to see complete bodies",
				}, help...)
			}
			if uint(len(messages)) == limit {
				help = append(help,
					"Run `"+axi.Binary()+` messages "`+inv.Arg(0)+`" --before `+messages[len(messages)-1].ID.String()+"` for older messages")
			}
			return axi.NewDoc().
				Set("channel", channel.Label).
				Set("count", strconv.Itoa(len(rows))+" messages").
				Set("messages", rows).
				Set("help", help), nil
		},
	}
}

func sendCommand() *axi.Command {
	return &axi.Command{
		Name: "send",
		Desc: "Post a message to a channel",
		Args: []axi.Arg{{Name: "channel", Required: true}},
		Flags: []axi.Flag{
			{Name: "--content", Value: "text", Desc: "Message body", Required: true},
			{Name: "--reply", Value: "message-id", Desc: "Reply to this message"},
			{Name: "--file", Value: "path", Desc: "Attach a file from disk"},
			{Name: "--silent", Desc: "Do not trigger push notifications"},
		},
		Examples: []string{
			`discord-axi send "My Server/general" --content "deploy finished"`,
			`discord-axi send 1234567890 --content "on it" --reply 9876543210`,
		},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			client, err := Client()
			if err != nil {
				return nil, err
			}
			channel, err := ResolveChannel(client, inv.Arg(0))
			if err != nil {
				return nil, err
			}

			data := api.SendMessageData{Content: inv.String("--content")}
			if raw := inv.String("--reply"); raw != "" {
				id, parseErr := discord.ParseSnowflake(raw)
				if parseErr != nil {
					return nil, axi.Usage(`--reply must be a message id, got "`+raw+`"`,
						"Run `"+axi.Binary()+` messages "`+inv.Arg(0)+`"`+"` and reuse an id from the output")
				}
				data.Reference = &discord.MessageReference{MessageID: discord.MessageID(id)}
			}
			if inv.Bool("--silent") {
				data.Flags |= discord.SuppressNotifications
			}
			if path := inv.String("--file"); path != "" {
				file, openErr := os.Open(path)
				if openErr != nil {
					return nil, axi.Usage(`--file cannot be read: `+path,
						"Pass a readable path, for example --file ./report.txt")
				}
				defer file.Close()
				data.Files = []sendpart.File{{Name: filepath.Base(path), Reader: file}}
			}

			message, err := client.SendMessageComplex(channel.Channel.ID, data)
			if err != nil {
				return nil, Translate(err)
			}
			return axi.NewDoc().
				Set("sent", "message posted to "+channel.Label).
				Set("id", message.ID.String()).
				Set("url", jumpURL(channel.Channel.GuildID, channel.Channel.ID, message.ID)), nil
		},
	}
}

func editCommand() *axi.Command {
	return &axi.Command{
		Name: "edit",
		Desc: "Edit a message this account sent",
		Args: []axi.Arg{{Name: "channel", Required: true}, {Name: "message", Required: true}},
		Flags: []axi.Flag{
			{Name: "--content", Value: "text", Desc: "Replacement body", Required: true},
		},
		Examples: []string{`discord-axi edit "My Server/general" 9876543210 --content "fixed typo"`},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			client, err := Client()
			if err != nil {
				return nil, err
			}
			channel, messageID, err := channelAndMessage(client, inv)
			if err != nil {
				return nil, err
			}
			message, err := client.EditMessage(channel.Channel.ID, messageID, inv.String("--content"))
			if err != nil {
				return nil, Translate(err)
			}
			return axi.NewDoc().
				Set("edited", "message "+message.ID.String()+" updated in "+channel.Label).
				Set("url", jumpURL(channel.Channel.GuildID, channel.Channel.ID, message.ID)), nil
		},
	}
}

func deleteCommand() *axi.Command {
	return &axi.Command{
		Name:     "delete",
		Desc:     "Delete a message",
		Args:     []axi.Arg{{Name: "channel", Required: true}, {Name: "message", Required: true}},
		Examples: []string{`discord-axi delete "My Server/general" 9876543210`},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			client, err := Client()
			if err != nil {
				return nil, err
			}
			channel, messageID, err := channelAndMessage(client, inv)
			if err != nil {
				return nil, err
			}
			// Already deleted is the state the agent asked for, so it is a no-op.
			if err := client.DeleteMessage(channel.Channel.ID, messageID, ""); err != nil {
				if NotFound(err) {
					return axi.NewDoc().
						Set("deleted", "message "+messageID.String()+" already gone from "+channel.Label+" (no-op)"), nil
				}
				return nil, Translate(err)
			}
			return axi.NewDoc().
				Set("deleted", "message "+messageID.String()+" removed from "+channel.Label), nil
		},
	}
}

func reactCommand() *axi.Command {
	return &axi.Command{
		Name: "react",
		Desc: "Add or remove this account's reaction on a message",
		Args: []axi.Arg{{Name: "channel", Required: true}, {Name: "message", Required: true}},
		Flags: []axi.Flag{
			{Name: "--emoji", Value: "emoji", Desc: `Unicode emoji, or "name:id" for a custom one`, Required: true},
			{Name: "--remove", Desc: "Remove the reaction instead of adding it"},
		},
		Examples: []string{
			`discord-axi react "My Server/general" 9876543210 --emoji "✅"`,
			`discord-axi react 1234567890 9876543210 --emoji "✅" --remove`,
		},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			client, err := Client()
			if err != nil {
				return nil, err
			}
			channel, messageID, err := channelAndMessage(client, inv)
			if err != nil {
				return nil, err
			}
			emoji := discord.APIEmoji(inv.String("--emoji"))

			if inv.Bool("--remove") {
				if err := client.Unreact(channel.Channel.ID, messageID, emoji); err != nil {
					if NotFound(err) {
						return axi.NewDoc().
							Set("react", string(emoji)+" was not on message "+messageID.String()+" (no-op)"), nil
					}
					return nil, Translate(err)
				}
				return axi.NewDoc().
					Set("react", string(emoji)+" removed from message "+messageID.String()), nil
			}
			if err := client.React(channel.Channel.ID, messageID, emoji); err != nil {
				return nil, Translate(err)
			}
			return axi.NewDoc().
				Set("react", string(emoji)+" added to message "+messageID.String()), nil
		},
	}
}

func searchCommand() *axi.Command {
	return &axi.Command{
		Name: "search",
		Desc: "Search a guild's message history (user accounts only)",
		Args: []axi.Arg{{Name: "guild", Required: true}},
		Flags: []axi.Flag{
			{Name: "--content", Value: "text", Desc: "Text to search for", Required: true},
			{Name: "--author", Value: "user-id", Desc: "Keep only messages from this author"},
			{Name: "--limit", Value: "n", Desc: "Max results to print", Default: "25"},
			{Name: "--full", Desc: "Print complete bodies instead of truncating"},
		},
		Examples: []string{`discord-axi search "My Server" --content "deploy failed"`},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			token, err := Token()
			if err != nil {
				return nil, err
			}
			if IsBot(token) {
				return nil, axi.Fail("FORBIDDEN", "Discord does not offer message search to bot accounts",
					"Run `"+axi.Binary()+` messages "<guild>/<channel>" --limit 100`+"` and filter the output")
			}
			limit, err := inv.Uint("--limit")
			if err != nil {
				return nil, err
			}
			client := NewClient(token)
			guild, err := ResolveGuild(client, inv.Arg(0))
			if err != nil {
				return nil, err
			}

			data := api.SearchData{Content: inv.String("--content")}
			if raw := inv.String("--author"); raw != "" {
				id, parseErr := discord.ParseSnowflake(raw)
				if parseErr != nil {
					return nil, axi.Usage(`--author must be a user id, got "`+raw+`"`,
						"Run `"+axi.Binary()+` messages "<guild>/<channel>" --fields url`+"` to find author ids")
				}
				data.AuthorID = discord.UserID(id)
			}

			response, err := client.Search(guild.ID, data)
			if err != nil {
				return nil, Translate(err)
			}
			hits := make([]discord.Message, 0, len(response.Messages))
			for _, group := range response.Messages {
				if len(group) > 0 {
					hits = append(hits, group[0])
				}
			}
			if len(hits) == 0 {
				return axi.NewDoc().
					Set("search", `0 messages matching "`+inv.String("--content")+`" in `+guild.Name), nil
			}

			shown := limited(hits, limit)
			rows := make([]*axi.Doc, 0, len(shown))
			for _, message := range shown {
				body, _ := truncate(message.Content, inv.Bool("--full"))
				rows = append(rows, axi.NewDoc().
					Set("id", message.ID.String()).
					Set("channel", message.ChannelID.String()).
					Set("author", message.Author.DisplayOrUsername()).
					Set("time", timestamp(message.Timestamp)).
					Set("content", body))
			}
			return axi.NewDoc().
				Set("guild", guild.Name).
				Set("count", countOf(len(shown), int(response.TotalResults))).
				Set("results", rows).
				Set("help", []string{
					"Run `" + axi.Binary() + " messages <channel-id> --before <id>` to read around a hit",
				}), nil
		},
	}
}

func channelAndMessage(client *api.Client, inv *axi.Invocation) (ChannelRef, discord.MessageID, error) {
	id, err := discord.ParseSnowflake(inv.Arg(1))
	if err != nil {
		return ChannelRef{}, 0, axi.Usage(`<message> must be a message id, got "`+inv.Arg(1)+`"`,
			"Run `"+axi.Binary()+` messages "`+inv.Arg(0)+`"`+"` and reuse an id from the output")
	}
	channel, err := ResolveChannel(client, inv.Arg(0))
	if err != nil {
		return ChannelRef{}, 0, err
	}
	return channel, discord.MessageID(id), nil
}

func isUnauthorized(err error) bool {
	var axiErr *axi.Error
	return errors.As(Translate(err), &axiErr) && axiErr.Code == "NOT_AUTHENTICATED"
}
