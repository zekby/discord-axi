package app

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

// A row with an empty content cell tells an agent nothing, and Discord sends
// plenty of messages that carry no text.
func TestDescribeNeverReturnsAnEmptyCell(t *testing.T) {
	cases := []struct {
		name    string
		message discord.Message
		want    string
	}{
		{
			"plain text",
			discord.Message{Content: "hello"},
			"hello",
		},
		{
			"sticker",
			discord.Message{Stickers: []discord.StickerItem{{Name: "Wave"}}},
			"[sticker: Wave]",
		},
		{
			"attachments",
			discord.Message{Attachments: []discord.Attachment{{Filename: "a.png"}, {Filename: "b.mp4"}}},
			"[2 attachment(s): a.png, b.mp4]",
		},
		{
			"embed with a title",
			discord.Message{Embeds: []discord.Embed{{Title: "Release 1.2"}}},
			"[embed: Release 1.2]",
		},
		{
			"embed with only a url",
			discord.Message{Embeds: []discord.Embed{{URL: "https://example.com"}}},
			"[embed: https://example.com]",
		},
		{
			"forwarded message",
			discord.Message{MessageSnapshots: []discord.MessageSnapshot{
				{Message: discord.MessageSnapshotMessage{Content: "original text"}},
			}},
			"[forwarded: original text]",
		},
		{
			"nothing at all",
			discord.Message{},
			"[no text]",
		},
		{
			"a system type this client does not know",
			discord.Message{Type: 67},
			"[system message, type 67]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := describe(tc.message); got != tc.want {
				t.Fatalf("describe() = %q, want %q", got, tc.want)
			}
			if describe(tc.message) == "" {
				t.Fatal("describe() must never return an empty string")
			}
		})
	}
}
