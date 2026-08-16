package axi

import (
	"strconv"
	"strings"
)

// renamedFlags points an agent at the replacement in one step instead of making
// it read the whole flag list again.
var renamedFlags = map[string]string{
	"--message": "--content",
	"--text":    "--content",
	"--body":    "--content",
	"--server":  "--guild",
	"--count":   "--limit",
	"--max":     "--limit",
	"--query":   "--content",
	"--channel": "--content",
}

type Arg struct {
	Name     string
	Required bool
}

type Flag struct {
	Name     string // including the leading dashes
	Value    string // placeholder; empty means the flag is a boolean
	Desc     string
	Default  string
	Required bool
}

func (f Flag) boolean() bool { return f.Value == "" }

type Command struct {
	Name string
	Desc string
	// Caution is a consequence the agent must weigh before running the command.
	// It is surfaced in `--help` and in the generated skill, never suppressed.
	Caution  string
	Args     []Arg
	Flags    []Flag
	Examples []string
	Run      func(*Invocation) (*Doc, error)
}

type Invocation struct {
	Command *Command
	// Raw is argv as given, for handlers that relay the command elsewhere.
	Raw        []string
	globals    []Flag
	Positional []string
	strings    map[string]string
	booleans   map[string]bool
}

// SyntheticInvocation carries flag values that did not come from argv, for
// callers that must resolve a global flag before a command spec exists.
func SyntheticInvocation(values map[string]string) *Invocation {
	inv := &Invocation{strings: map[string]string{}, booleans: map[string]bool{}}
	for name, value := range values {
		inv.strings[name] = value
	}
	return inv
}

func (i *Invocation) Arg(index int) string {
	if index < len(i.Positional) {
		return i.Positional[index]
	}
	return ""
}

func (i *Invocation) String(flag string) string { return i.strings[flag] }

func (i *Invocation) Bool(flag string) bool { return i.booleans[flag] }

func (i *Invocation) Has(flag string) bool {
	_, ok := i.strings[flag]
	return ok
}

// Uint returns a positive integer flag, defaulting to the spec's default.
func (i *Invocation) Uint(flag string) (uint, error) {
	raw, ok := i.strings[flag]
	if !ok || raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || value == 0 {
		return 0, Usage(
			flag+` must be a positive integer, got "`+raw+`"`,
			"Run `"+Binary()+" "+i.Command.Name+" "+flag+" 30`",
		)
	}
	return uint(value), nil
}

// Parse validates argv against the command spec before any dependency call. An
// unrecognized flag is rejected by name rather than silently dropped. Globals
// are flags the whole CLI accepts, so they are valid on every command.
func (c *Command) Parse(args []string, globals ...Flag) (*Invocation, error) {
	inv := &Invocation{
		Command:  c,
		Raw:      args,
		globals:  globals,
		strings:  map[string]string{},
		booleans: map[string]bool{},
	}
	for _, flag := range append(append([]Flag{}, c.Flags...), globals...) {
		if flag.Default != "" {
			inv.strings[flag.Name] = flag.Default
		}
	}

	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !strings.HasPrefix(arg, "-") {
			inv.Positional = append(inv.Positional, arg)
			continue
		}

		name, inline, hasInline := strings.Cut(arg, "=")
		flag, known := c.flag(name, globals)
		if !known {
			return nil, c.unknownFlag(name, globals)
		}
		if flag.boolean() {
			if hasInline {
				return nil, Usage(name+" does not take a value",
					"Run `"+Binary()+" "+c.Name+" "+name+"`")
			}
			inv.booleans[name] = true
			continue
		}
		value := inline
		if !hasInline {
			if index+1 >= len(args) {
				return nil, Usage(name+" requires a value",
					"Run `"+Binary()+" "+c.Name+" "+name+` "<`+flag.Value+`>"`+"`")
			}
			index++
			value = args[index]
		}
		inv.strings[name] = value
	}

	if extra := len(inv.Positional) - len(c.Args); extra > 0 {
		return nil, Usage(
			`unexpected argument "`+inv.Positional[len(c.Args)]+`" for `+"`"+c.Name+"`",
			"`"+c.Name+"` takes "+strconv.Itoa(len(c.Args))+" argument(s): "+c.argUsage(),
		)
	}
	for index, arg := range c.Args {
		if arg.Required && index >= len(inv.Positional) {
			return nil, Usage("<"+arg.Name+"> is required for `"+c.Name+"`",
				"Run `"+c.Usage()+"`")
		}
	}
	for _, flag := range c.Flags {
		if flag.Required && inv.strings[flag.Name] == "" {
			return nil, Usage(flag.Name+" is required for `"+c.Name+"`",
				"Run `"+c.Usage()+"`")
		}
	}
	return inv, nil
}

func (c *Command) flag(name string, globals []Flag) (Flag, bool) {
	for _, flag := range c.Flags {
		if flag.Name == name {
			return flag, true
		}
	}
	for _, flag := range globals {
		if flag.Name == name {
			return flag, true
		}
	}
	return Flag{}, false
}

// unknownFlag folds the command's flag reference into the error so the agent
// corrects itself without a second `--help` call.
func (c *Command) unknownFlag(name string, globals []Flag) error {
	if replacement, ok := renamedFlags[name]; ok {
		if _, valid := c.flag(replacement, globals); valid {
			return Usage("unknown flag "+name+" for `"+c.Name+"`",
				name+" is not a flag of this CLI; use "+replacement+" instead")
		}
	}
	help := []string{"valid flags for `" + c.Name + "`: " + c.flagNames() + " (--help always allowed)"}
	for _, flag := range c.Flags {
		help = append(help, flag.Name+flagPlaceholder(flag)+" — "+flag.Desc)
	}
	for _, flag := range globals {
		help = append(help, flag.Name+flagPlaceholder(flag)+" — "+flag.Desc+" [any command]")
	}
	return Usage("unknown flag "+name+" for `"+c.Name+"`", help...)
}

func flagPlaceholder(flag Flag) string {
	if flag.boolean() {
		return ""
	}
	return " <" + flag.Value + ">"
}

func (c *Command) flagNames() string {
	if len(c.Flags) == 0 {
		return "none"
	}
	names := make([]string, len(c.Flags))
	for i, flag := range c.Flags {
		names[i] = flag.Name
	}
	return strings.Join(names, ", ")
}

func (c *Command) argUsage() string {
	if len(c.Args) == 0 {
		return "none"
	}
	parts := make([]string, len(c.Args))
	for i, arg := range c.Args {
		parts[i] = "<" + arg.Name + ">"
	}
	return strings.Join(parts, " ")
}

// Usage is the shortest complete invocation: binary, name, arguments and every
// required flag.
func (c *Command) Usage() string {
	parts := []string{Binary(), c.Name}
	for _, arg := range c.Args {
		parts = append(parts, "<"+arg.Name+">")
	}
	for _, flag := range c.Flags {
		if flag.Required {
			parts = append(parts, flag.Name+` "<`+flag.Value+`>"`)
		}
	}
	return strings.Join(parts, " ")
}

// Help is the per-command reference: flags with defaults, arguments, examples.
func (c *Command) Help(globals ...Flag) *Doc {
	doc := NewDoc().
		Set("command", c.Name).
		Set("description", c.Desc).
		Set("usage", c.Usage())
	if c.Caution != "" {
		doc.Set("caution", c.Caution)
	}
	if len(c.Args) > 0 {
		args := make([]*Doc, 0, len(c.Args))
		for _, arg := range c.Args {
			required := "required"
			if !arg.Required {
				required = "optional"
			}
			args = append(args, NewDoc().Set("name", arg.Name).Set("required", required))
		}
		doc.Set("arguments", args)
	}
	if len(c.Flags) > 0 {
		flags := NewDoc()
		for _, flag := range c.Flags {
			desc := flag.Desc
			if flag.Default != "" {
				desc += " [default " + flag.Default + "]"
			}
			if flag.Required {
				desc += " [required]"
			}
			flags.Set(flag.Name+flagPlaceholder(flag), desc)
		}
		doc.Set("flags", flags)
	}
	if len(globals) > 0 {
		names := make([]string, len(globals))
		for i, flag := range globals {
			names[i] = flag.Name + flagPlaceholder(flag)
		}
		doc.Set("globals", strings.Join(names, ", ")+" (accepted by every command)")
	}
	return doc.Set("examples", c.Examples)
}
