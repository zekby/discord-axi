package axi

import (
	"strings"
	"testing"
)

func testCommand() *Command {
	return &Command{
		Name: "messages",
		Desc: "Read recent messages",
		Args: []Arg{{Name: "channel", Required: true}},
		Flags: []Flag{
			{Name: "--limit", Value: "n", Desc: "Messages to fetch", Default: "30"},
			{Name: "--content", Value: "text", Desc: "Message body"},
			{Name: "--full", Desc: "Print complete bodies"},
		},
		Examples: []string{"discord-axi messages general"},
	}
}

func TestParseRejectsUnknownFlagWithInlineReference(t *testing.T) {
	_, err := testCommand().Parse([]string{"general", "--limt", "5"})
	if err == nil {
		t.Fatal("expected an error for an unknown flag")
	}
	if ExitCode(err) != ExitUsage {
		t.Fatalf("exit code = %d, want %d", ExitCode(err), ExitUsage)
	}
	rendered := ErrorDoc(err).Encode()
	if !strings.Contains(rendered, "unknown flag --limt for `messages`") {
		t.Fatalf("error does not name the flag:\n%s", rendered)
	}
	if !strings.Contains(rendered, "--limit, --content, --full") {
		t.Fatalf("error does not list valid flags inline:\n%s", rendered)
	}
}

func TestParsePointsRenamedFlagAtItsReplacement(t *testing.T) {
	_, err := testCommand().Parse([]string{"general", "--message", "hi"})
	if err == nil {
		t.Fatal("expected an error for a renamed flag")
	}
	if rendered := ErrorDoc(err).Encode(); !strings.Contains(rendered, "use --content instead") {
		t.Fatalf("error does not point at the replacement:\n%s", rendered)
	}
}

func TestParseRequiresDeclaredArguments(t *testing.T) {
	_, err := testCommand().Parse(nil)
	if err == nil || !strings.Contains(err.Error(), "<channel> is required") {
		t.Fatalf("err = %v, want a missing argument error", err)
	}
}

func TestParseRejectsExtraPositional(t *testing.T) {
	_, err := testCommand().Parse([]string{"general", "extra"})
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "extra"`) {
		t.Fatalf("err = %v, want an unexpected argument error", err)
	}
}

func TestParseAppliesDefaultsAndValueForms(t *testing.T) {
	inv, err := testCommand().Parse([]string{"general", "--content=hello world", "--full"})
	if err != nil {
		t.Fatal(err)
	}
	limit, err := inv.Uint("--limit")
	if err != nil || limit != 30 {
		t.Fatalf("limit = %d, err = %v, want 30", limit, err)
	}
	if got := inv.String("--content"); got != "hello world" {
		t.Fatalf("content = %q", got)
	}
	if !inv.Bool("--full") {
		t.Fatal("--full was not recorded")
	}
	if inv.Arg(0) != "general" {
		t.Fatalf("arg = %q", inv.Arg(0))
	}
}

func TestUintRejectsNonPositiveValues(t *testing.T) {
	inv, err := testCommand().Parse([]string{"general", "--limit", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inv.Uint("--limit"); err == nil || ExitCode(err) != ExitUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestAppRunDispatchesAndReportsExitCodes(t *testing.T) {
	command := testCommand()
	command.Run = func(inv *Invocation) (*Doc, error) {
		return NewDoc().Set("channel", inv.Arg(0)), nil
	}
	app := &App{
		Name:        "discord-axi",
		Description: "test",
		Version:     "9.9.9",
		Home:        func() (*Doc, error) { return NewDoc().Set("account", "nobody"), nil },
		Commands:    []*Command{command},
	}

	cases := []struct {
		name     string
		argv     []string
		wantCode int
		contains string
	}{
		{"version", []string{"--version"}, ExitOK, "9.9.9"},
		{"home header", nil, ExitOK, "bin: "},
		{"dispatch", []string{"messages", "general"}, ExitOK, "channel: general"},
		{"command help", []string{"messages", "--help"}, ExitOK, "command: messages"},
		{"unknown command", []string{"mesages"}, ExitUsage, "unknown command: mesages"},
		{"leading flag", []string{"--limit", "5"}, ExitUsage, "flags must come after the command"},
		{"unknown flag", []string{"messages", "general", "--nope"}, ExitUsage, "unknown flag --nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			if code := app.Run(tc.argv, &out); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (output: %s)", code, tc.wantCode, out.String())
			}
			if !strings.Contains(out.String(), tc.contains) {
				t.Fatalf("output does not contain %q:\n%s", tc.contains, out.String())
			}
		})
	}
}
