// Command discord-axi exposes Discord to coding agents as an AXI-compliant CLI.
// It talks to Discord through the same stack Discordo uses: arikawa for the
// REST API, ningen for read state, and Discordo's browser-shaped transport.
package main

import (
	"io"
	"log/slog"
	"os"

	"github.com/zekby/discord-axi/internal/app"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "0.1.0"

func main() {
	// Dependencies log through slog; stdout carries structured output only.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	os.Exit(app.App(version).Run(os.Args[1:], os.Stdout))
}
