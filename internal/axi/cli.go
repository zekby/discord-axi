package axi

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

var binary = "axi"

// Binary is the command name used in every suggestion this CLI prints.
func Binary() string { return binary }

type App struct {
	Name        string
	Description string
	Version     string
	// Home runs with no arguments and shows live content, not a manual.
	Home     func() (*Doc, error)
	Commands []*Command
	// HelpNotes are shown under the top-level command list.
	HelpNotes []string
}

func (a *App) command(name string) *Command {
	for _, command := range a.Commands {
		if command.Name == name {
			return command
		}
	}
	return nil
}

// Run dispatches argv and returns the process exit code. Structured output and
// structured errors both go to out (stdout); nothing else is printed there.
func (a *App) Run(argv []string, out io.Writer) int {
	binary = a.Name

	if len(argv) == 1 {
		switch argv[0] {
		case "-v", "-V", "--version":
			fmt.Fprintln(out, a.Version)
			return ExitOK
		case "--help", "-h":
			return write(out, a.Help(), ExitOK)
		}
	}

	if len(argv) == 0 {
		doc, err := a.Home()
		if err != nil {
			return write(out, mergeDocs(HomeHeader(a.Description), ErrorDoc(err)), ExitCode(err))
		}
		return write(out, mergeDocs(HomeHeader(a.Description), doc), ExitOK)
	}

	name := argv[0]
	if strings.HasPrefix(name, "-") {
		return write(out, ErrorDoc(Usage(
			"flags must come after the command",
			"Run `"+a.Name+" <command> [args] [flags]`",
			"Move `"+name+"` after the command instead of before it",
		)), ExitUsage)
	}

	command := a.command(name)
	if command == nil {
		return write(out, ErrorDoc(Usage(
			"unknown command: "+name,
			"valid commands: "+strings.Join(a.commandNames(), ", "),
			"Run `"+a.Name+" --help` for the usage of each command",
		)), ExitUsage)
	}

	args := argv[1:]
	if slices.Contains(args, "--help") || slices.Contains(args, "-h") {
		return write(out, command.Help(), ExitOK)
	}

	invocation, err := command.Parse(args)
	if err != nil {
		return write(out, ErrorDoc(err), ExitCode(err))
	}
	doc, err := command.Run(invocation)
	if err != nil {
		return write(out, ErrorDoc(err), ExitCode(err))
	}
	return write(out, doc, ExitOK)
}

func (a *App) commandNames() []string {
	names := make([]string, len(a.Commands))
	for i, command := range a.Commands {
		names[i] = command.Name
	}
	return names
}

func (a *App) Help() *Doc {
	rows := make([]*Doc, 0, len(a.Commands))
	for _, command := range a.Commands {
		rows = append(rows, NewDoc().
			Set("name", command.Name).
			Set("usage", command.Usage()).
			Set("description", command.Desc))
	}
	return NewDoc().
		Set(a.Name, a.Description).
		Set("version", a.Version).
		Set("commands", rows).
		Set("help", a.HelpNotes)
}

func mergeDocs(docs ...*Doc) *Doc {
	merged := NewDoc()
	for _, doc := range docs {
		if doc == nil {
			continue
		}
		for i, key := range doc.keys {
			merged.Set(key, doc.values[i])
		}
	}
	return merged
}

func write(out io.Writer, doc *Doc, code int) int {
	fmt.Fprintln(out, doc.Encode())
	return code
}
