package axi

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Exit codes: 0 success (including no-ops), 1 error, 2 usage error.
const (
	ExitOK    = 0
	ExitError = 1
	ExitUsage = 2
)

const CodeValidation = "VALIDATION_ERROR"

// Error is a structured failure. It is printed to stdout in TOON, like every
// other result, so an agent can read and act on it.
type Error struct {
	Message string
	Code    string
	Help    []string
}

func (e *Error) Error() string { return e.Message }

// Usage reports an unusable invocation: unknown flag, missing argument, bad
// value. It exits 2 and never reaches the network.
func Usage(message string, help ...string) *Error {
	return &Error{Message: message, Code: CodeValidation, Help: help}
}

// Fail reports a runtime failure with a domain-specific code.
func Fail(code, message string, help ...string) *Error {
	return &Error{Message: message, Code: code, Help: help}
}

// Relayed is a failure whose document was already rendered by another process
// running this same CLI. Re-wrapping it would nest one structured error inside
// the message of another, so it is passed through with its code and exit status.
type Relayed struct {
	Doc  *Doc
	Code int
}

func (r *Relayed) Error() string { return "relayed failure" }

// Relay wraps an already-rendered failure document for pass-through.
func Relay(doc *Doc, code int) *Relayed { return &Relayed{Doc: doc, Code: code} }

func ExitCode(err error) int {
	var relayed *Relayed
	if errors.As(err, &relayed) {
		return relayed.Code
	}
	var axiErr *Error
	if errors.As(err, &axiErr) && axiErr.Code == CodeValidation {
		return ExitUsage
	}
	return ExitError
}

func ErrorDoc(err error) *Doc {
	doc := NewDoc()
	var relayed *Relayed
	if errors.As(err, &relayed) {
		return relayed.Doc
	}
	var axiErr *Error
	if errors.As(err, &axiErr) {
		doc.Set("error", axiErr.Message).Set("code", axiErr.Code)
		if len(axiErr.Help) > 0 {
			doc.Set("help", axiErr.Help)
		}
		return doc
	}
	return doc.Set("error", err.Error()).Set("code", "UNKNOWN")
}

// HomeHeader identifies the executable before any live data, so an agent that
// sees the output in ambient context knows what produced it.
func HomeHeader(description string) *Doc {
	return NewDoc().Set("bin", CollapseHome(ExecPath())).Set("description", description)
}

func ExecPath() string {
	path, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func CollapseHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home) {
		return path
	}
	return "~" + path[len(home):]
}
