package app

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/zekby/discord-axi/internal/axi"
)

// GuildRef is the minimum an agent needs to address a guild again.
type GuildRef struct {
	ID   discord.GuildID
	Name string
}

func isSnowflake(value string) bool {
	if len(value) < 15 || len(value) > 25 {
		return false
	}
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil
}

// ResolveGuild accepts a guild id or a name. A name that matches several guilds
// is an error listing the candidates, never a guess.
func ResolveGuild(client *api.Client, reference string) (GuildRef, error) {
	if isSnowflake(reference) {
		id, _ := discord.ParseSnowflake(reference)
		guild, err := client.Guild(discord.GuildID(id))
		if err != nil {
			return GuildRef{}, Translate(err)
		}
		return GuildRef{ID: guild.ID, Name: guild.Name}, nil
	}

	guilds, err := client.Guilds(0)
	if err != nil {
		return GuildRef{}, Translate(err)
	}
	matches := matchGuilds(guilds, reference)
	switch len(matches) {
	case 1:
		return GuildRef{ID: matches[0].ID, Name: matches[0].Name}, nil
	case 0:
		return GuildRef{}, axi.Fail("NOT_FOUND", `no guild matching "`+reference+`"`,
			"Run `"+axi.Binary()+" guilds` to list reachable guilds")
	default:
		help := make([]string, 0, len(matches))
		for _, guild := range matches {
			help = append(help, guild.Name+" (id "+guild.ID.String()+")")
		}
		return GuildRef{}, axi.Usage(
			`"`+reference+`" matches `+strconv.Itoa(len(matches))+" guilds", help...)
	}
}

func matchGuilds(guilds []discord.Guild, reference string) []discord.Guild {
	wanted := strings.ToLower(reference)
	var exact, partial []discord.Guild
	for _, guild := range guilds {
		name := strings.ToLower(guild.Name)
		switch {
		case name == wanted:
			exact = append(exact, guild)
		case strings.Contains(name, wanted):
			partial = append(partial, guild)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

// ChannelRef carries the channel plus the label the agent used to reach it, so
// suggestions can echo the same addressing form back.
type ChannelRef struct {
	Channel discord.Channel
	Label   string
}

// ResolveChannel accepts a channel id, a "<guild>/<channel>" path, or a DM name.
func ResolveChannel(client *api.Client, reference string) (ChannelRef, error) {
	if isSnowflake(reference) {
		id, _ := discord.ParseSnowflake(reference)
		channel, err := client.Channel(discord.ChannelID(id))
		if err != nil {
			return ChannelRef{}, Translate(err)
		}
		return ChannelRef{Channel: *channel, Label: ChannelName(*channel)}, nil
	}

	if guildPart, channelPart, ok := strings.Cut(reference, "/"); ok {
		guild, err := ResolveGuild(client, guildPart)
		if err != nil {
			return ChannelRef{}, err
		}
		channels, err := client.Channels(guild.ID)
		if err != nil {
			return ChannelRef{}, Translate(err)
		}
		wanted := strings.TrimPrefix(channelPart, "#")
		matches := matchChannels(readable(channels), wanted)
		switch len(matches) {
		case 1:
			return ChannelRef{Channel: matches[0], Label: guild.Name + "/" + matches[0].Name}, nil
		case 0:
			return ChannelRef{}, axi.Fail("NOT_FOUND", `no channel matching "`+wanted+`" in `+guild.Name,
				"Run `"+axi.Binary()+` channels "`+guild.Name+`"`+"` to list channels")
		default:
			help := make([]string, 0, len(matches))
			for _, channel := range matches {
				help = append(help, guild.Name+"/"+channel.Name+" (id "+channel.ID.String()+")")
			}
			return ChannelRef{}, axi.Usage(
				`"`+reference+`" matches `+strconv.Itoa(len(matches))+" channels", help...)
		}
	}

	channels, err := client.PrivateChannels()
	if err != nil {
		return ChannelRef{}, axi.Fail("NOT_FOUND", `"`+reference+`" is not a channel id or a "<guild>/<channel>" path`,
			"Run `"+axi.Binary()+" guilds` then `"+axi.Binary()+` channels "<guild>"`+"` to find the id")
	}
	matches := matchDirectMessages(channels, strings.TrimPrefix(reference, "@"))
	switch len(matches) {
	case 1:
		return ChannelRef{Channel: matches[0], Label: ChannelName(matches[0])}, nil
	case 0:
		return ChannelRef{}, axi.Fail("NOT_FOUND", `no channel matching "`+reference+`"`,
			"Use a channel id or a \"<guild>/<channel>\" path",
			"Run `"+axi.Binary()+" dms` to list direct messages")
	default:
		help := make([]string, 0, len(matches))
		for _, channel := range matches {
			help = append(help, ChannelName(channel)+" (id "+channel.ID.String()+")")
		}
		return ChannelRef{}, axi.Usage(
			`"`+reference+`" matches `+strconv.Itoa(len(matches))+" channels", help...)
	}
}

func matchChannels(channels []discord.Channel, reference string) []discord.Channel {
	wanted := strings.ToLower(reference)
	var exact, partial []discord.Channel
	for _, channel := range channels {
		name := strings.ToLower(channel.Name)
		switch {
		case name == wanted:
			exact = append(exact, channel)
		case strings.Contains(name, wanted):
			partial = append(partial, channel)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

func matchDirectMessages(channels []discord.Channel, reference string) []discord.Channel {
	wanted := strings.ToLower(reference)
	var exact, partial []discord.Channel
	for _, channel := range channels {
		name := strings.ToLower(ChannelName(channel))
		switch {
		case name == wanted:
			exact = append(exact, channel)
		case strings.Contains(name, wanted):
			partial = append(partial, channel)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return partial
}

// ChannelName renders a channel the way Discordo labels it: guild channels by
// name, DMs by their recipients.
func ChannelName(channel discord.Channel) string {
	if channel.Type == discord.DirectMessage || channel.Type == discord.GroupDM {
		if channel.Name != "" {
			return channel.Name
		}
		recipients := make([]string, len(channel.DMRecipients))
		for i, recipient := range channel.DMRecipients {
			recipients[i] = recipient.DisplayOrUsername()
		}
		return strings.Join(recipients, ", ")
	}
	if channel.Name == "" {
		return "channel-" + channel.ID.String()
	}
	return channel.Name
}

// readable drops channels that hold no messages, so a channel list stays a list
// of things an agent can actually read or post to.
func readable(channels []discord.Channel) []discord.Channel {
	kept := channels[:0:0]
	for _, channel := range channels {
		switch channel.Type {
		case discord.GuildCategory, discord.GuildVoice, discord.GuildStageVoice:
			continue
		}
		kept = append(kept, channel)
	}
	return kept
}

// SortGuildChannels orders channels the way Discord displays them.
func SortGuildChannels(channels []discord.Channel) {
	slices.SortFunc(channels, func(a, b discord.Channel) int {
		return cmp.Compare(a.Position, b.Position)
	})
}

// SortPrivateChannels puts the most recently active conversation first.
func SortPrivateChannels(channels []discord.Channel) {
	slices.SortFunc(channels, func(a, b discord.Channel) int {
		return cmp.Compare(lastMessage(b), lastMessage(a))
	})
}

func lastMessage(channel discord.Channel) discord.MessageID {
	if channel.LastMessageID.IsValid() {
		return channel.LastMessageID
	}
	return discord.MessageID(channel.ID)
}

// ChannelKind is the short type name printed in listings.
func ChannelKind(channel discord.Channel) string {
	switch channel.Type {
	case discord.GuildText:
		return "text"
	case discord.DirectMessage:
		return "dm"
	case discord.GuildVoice:
		return "voice"
	case discord.GroupDM:
		return "group-dm"
	case discord.GuildCategory:
		return "category"
	case discord.GuildAnnouncement:
		return "announcement"
	case discord.GuildAnnouncementThread, discord.GuildPublicThread, discord.GuildPrivateThread:
		return "thread"
	case discord.GuildStageVoice:
		return "stage"
	case discord.GuildForum:
		return "forum"
	default:
		return "type-" + strconv.Itoa(int(channel.Type))
	}
}
