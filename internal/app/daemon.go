package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ayn2op/ningen/v3"
	"github.com/zekby/discord-axi/internal/axi"
)

// The official client opens one gateway connection and keeps it open for hours.
// Connecting and disconnecting for every command is the opposite of that, so the
// daemon holds a single session and answers commands over a unix socket.
const daemonDialTimeout = 3 * time.Second

// liveState is non-nil only inside the daemon process, where a gateway session
// is already open. Commands use it instead of connecting on their own.
var liveState *ningen.State

// daemonServed lists the commands that need a gateway session. Everything else
// is a plain REST call and gains nothing from being relayed.
var daemonServed = map[string]bool{
	"unread": true,
	"read":   true,
}

type daemonRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type daemonResponse struct {
	Output string `json:"output"`
	Code   int    `json:"code"`
}

// forwardToDaemon hands a command to a running daemon. It reports false when no
// daemon is listening, so the caller falls back to doing the work itself.
func forwardToDaemon(command string, args []string) (*axi.Doc, bool, error) {
	if liveState != nil || !daemonServed[command] {
		return nil, false, nil
	}
	connection, err := net.DialTimeout("unix", SocketPath(), daemonDialTimeout)
	if err != nil {
		return nil, false, nil
	}
	defer connection.Close()

	if err := json.NewEncoder(connection).Encode(daemonRequest{Command: command, Args: args}); err != nil {
		return nil, false, nil
	}
	var response daemonResponse
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return nil, false, nil
	}
	if response.Code != axi.ExitOK {
		return nil, true, axi.Fail("DAEMON_ERROR", strings.TrimSpace(response.Output))
	}
	return axi.Raw(response.Output), true, nil
}

func daemonCommand() *axi.Command {
	return &axi.Command{
		Name: "daemon",
		Desc: "Hold one long-lived gateway connection and serve read commands from it",
		Args: []axi.Arg{{Name: "action", Required: true}},
		Examples: []string{
			"discord-axi daemon run",
			"discord-axi daemon status",
			"discord-axi daemon stop",
		},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			switch inv.Arg(0) {
			case "run":
				return runDaemon()
			case "status":
				return daemonStatus()
			case "stop":
				return stopDaemon()
			default:
				return nil, axi.Usage(`unknown daemon action "`+inv.Arg(0)+`"`,
					"Run `"+axi.Binary()+" daemon run` to start it in the foreground",
					"Run `"+axi.Binary()+" daemon status` to check whether one is running",
					"Run `"+axi.Binary()+" daemon stop` to shut it down")
			}
		},
	}
}

func daemonStatus() (*axi.Doc, error) {
	connection, err := net.DialTimeout("unix", SocketPath(), daemonDialTimeout)
	if err != nil {
		return axi.NewDoc().
			Set("daemon", "not running").
			Set("socket", axi.CollapseHome(SocketPath())).
			Set("help", []string{
				"Run `" + axi.Binary() + " daemon run` to keep one gateway connection open",
				"Read commands work without it, they just connect and disconnect each time",
			}), nil
	}
	defer connection.Close()

	_ = json.NewEncoder(connection).Encode(daemonRequest{Command: "__ping"})
	var response daemonResponse
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		return nil, axi.Fail("DAEMON_ERROR", "a socket exists but the daemon did not answer",
			"Run `"+axi.Binary()+" daemon stop` and start it again")
	}
	return axi.NewDoc().
		Set("daemon", strings.TrimSpace(response.Output)).
		Set("socket", axi.CollapseHome(SocketPath())).
		Set("help", []string{"Run `" + axi.Binary() + " unread` to use the live session"}), nil
}

func stopDaemon() (*axi.Doc, error) {
	connection, err := net.DialTimeout("unix", SocketPath(), daemonDialTimeout)
	if err != nil {
		_ = os.Remove(SocketPath())
		return axi.NewDoc().Set("daemon", "not running (no-op)"), nil
	}
	defer connection.Close()

	_ = json.NewEncoder(connection).Encode(daemonRequest{Command: "__stop"})
	var response daemonResponse
	_ = json.NewDecoder(connection).Decode(&response)
	return axi.NewDoc().Set("daemon", "stopped"), nil
}

func runDaemon() (*axi.Doc, error) {
	token, err := Token()
	if err != nil {
		return nil, err
	}
	if existing, err := net.DialTimeout("unix", SocketPath(), daemonDialTimeout); err == nil {
		existing.Close()
		return nil, axi.Fail("DAEMON_RUNNING", "a daemon is already listening on "+axi.CollapseHome(SocketPath()),
			"Run `"+axi.Binary()+" daemon status` to inspect it",
			"Run `"+axi.Binary()+" daemon stop` to replace it")
	}
	_ = os.Remove(SocketPath())

	connected, closeState, err := openState(token)
	if err != nil {
		return nil, err
	}
	defer closeState()
	liveState = connected

	listener, err := net.Listen("unix", SocketPath())
	if err != nil {
		return nil, axi.Fail("DAEMON_FAILED", "could not create the daemon socket",
			"Check write permission on "+axi.CollapseHome(SocketPath()))
	}
	defer os.Remove(SocketPath())
	_ = os.Chmod(SocketPath(), 0o600)

	started := time.Now()
	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-signals:
		case <-stop:
		}
		listener.Close()
	}()

	// Progress belongs on stderr; stdout stays a single structured document.
	fmt.Fprintln(os.Stderr, "discord-axi daemon listening on "+SocketPath())

	for {
		connection, err := listener.Accept()
		if err != nil {
			break
		}
		if serveDaemonConnection(connection, started, stop) {
			break
		}
	}
	return axi.NewDoc().
		Set("daemon", "stopped after "+time.Since(started).Round(time.Second).String()), nil
}

// serveDaemonConnection answers one request and reports whether the daemon was
// asked to shut down.
func serveDaemonConnection(connection net.Conn, started time.Time, stop chan struct{}) bool {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(30 * time.Second))

	var request daemonRequest
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&request); err != nil {
		return false
	}

	switch request.Command {
	case "__ping":
		reply(connection, "running, connected for "+time.Since(started).Round(time.Second).String(), axi.ExitOK)
		return false
	case "__stop":
		reply(connection, "stopping", axi.ExitOK)
		close(stop)
		return true
	}

	if !daemonServed[request.Command] {
		reply(connection, "the daemon does not serve `"+request.Command+"`", axi.ExitUsage)
		return false
	}

	var out strings.Builder
	code := App("daemon").Run(append([]string{request.Command}, request.Args...), &out)
	reply(connection, out.String(), code)
	return false
}

func reply(connection net.Conn, output string, code int) {
	_ = json.NewEncoder(connection).Encode(daemonResponse{Output: output, Code: code})
}
