package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ayn2op/ningen/v3"
	"github.com/zekby/discord-axi/internal/axi"
)

// The official client opens one gateway connection and keeps it open for hours.
// Connecting and disconnecting for every command is the opposite of that, so the
// daemon holds a single session and answers commands over a unix socket.
const (
	daemonDialTimeout = 3 * time.Second
	// defaultIdleTimeout stops an auto-started daemon from living forever after
	// the agent that wanted it has moved on.
	defaultIdleTimeout = 30 * time.Minute
	// startupTimeout covers connecting and the initial sync, which is the slow
	// part on accounts with many guilds.
	startupTimeout = 45 * time.Second
	// NoDaemonEnvVar switches off auto-start for anyone who does not want a
	// background process appearing on their machine.
	NoDaemonEnvVar = "DISCORD_AXI_NO_DAEMON"
)

func autoStartEnabled() bool {
	return os.Getenv(NoDaemonEnvVar) == ""
}

func idleTimeout(inv *axi.Invocation) (time.Duration, error) {
	raw := inv.String("--idle")
	minutes, err := strconv.Atoi(raw)
	if err != nil || minutes < 0 {
		return 0, axi.Usage(`--idle must be a whole number of minutes, got "`+raw+`"`,
			"Run `"+axi.Binary()+" daemon start --idle 30`",
			"Run `"+axi.Binary()+" daemon start --idle 0` to stay up until stopped")
	}
	return time.Duration(minutes) * time.Minute, nil
}

func daemonLogPath() string { return filepath.Join(StateDir(), "daemon.log") }

// startDaemon launches a detached daemon and waits until it is answering, so the
// caller can use it immediately. It is a no-op when one is already listening.
func startDaemon(idle time.Duration) error {
	if connection, err := net.DialTimeout("unix", SocketPath(), daemonDialTimeout); err == nil {
		connection.Close()
		return nil
	}
	// Fail here rather than spawning a child that can only die at the login step.
	token, err := Token()
	if err != nil {
		return err
	}
	if IsBot(token) {
		return axi.Fail("FORBIDDEN", "a bot token has no gateway read state to hold open")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(daemonLogPath(), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()

	command := exec.Command(executable, "daemon", "run", "--idle", strconv.Itoa(int(idle/time.Minute)))
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = detachAttrs()
	if err := command.Start(); err != nil {
		return err
	}
	// The child has its own session, so it outlives this process either way.
	// Watching it here only makes a failed start fail fast instead of waiting out
	// the whole startup window.
	exited := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(exited)
	}()

	deadline := time.Now().Add(startupTimeout)
	for time.Now().Before(deadline) {
		if connection, err := net.DialTimeout("unix", SocketPath(), daemonDialTimeout); err == nil {
			connection.Close()
			return nil
		}
		select {
		case <-exited:
			return axi.Fail("DAEMON_FAILED", "the daemon exited while starting up")
		case <-time.After(250 * time.Millisecond):
		}
	}
	return os.ErrDeadlineExceeded
}

func startDaemonCommand(idle time.Duration) (*axi.Doc, error) {
	if connection, err := net.DialTimeout("unix", SocketPath(), daemonDialTimeout); err == nil {
		connection.Close()
		return axi.NewDoc().
			Set("daemon", "already running (no-op)").
			Set("socket", axi.CollapseHome(SocketPath())), nil
	}
	if err := startDaemon(idle); err != nil {
		return nil, axi.Fail("DAEMON_FAILED", "the daemon did not come up",
			"Read "+axi.CollapseHome(daemonLogPath())+" for what it reported",
			"Run `"+axi.Binary()+" daemon run` to watch it start in the foreground")
	}
	return axi.NewDoc().
		Set("daemon", "started in the background").
		Set("socket", axi.CollapseHome(SocketPath())).
		Set("idle", idleDescription(idle)).
		Set("log", axi.CollapseHome(daemonLogPath())).
		Set("help", []string{
			"Run `" + axi.Binary() + " unread` to use the live session",
			"Run `" + axi.Binary() + " daemon stop` to shut it down",
		}), nil
}

func idleDescription(idle time.Duration) string {
	if idle == 0 {
		return "stays up until stopped"
	}
	return "shuts down after " + idle.String() + " without a request"
}

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

// forwardToDaemon hands a command to a running daemon, starting one first if
// none is listening. It reports false only when the caller must do the work
// itself, which now happens only if auto-start is switched off or fails.
func forwardToDaemon(command string, args []string) (*axi.Doc, bool, error) {
	if liveState != nil || !daemonServed[command] {
		return nil, false, nil
	}
	connection, err := net.DialTimeout("unix", SocketPath(), daemonDialTimeout)
	if err != nil {
		if !autoStartEnabled() {
			return nil, false, nil
		}
		if err := startDaemon(defaultIdleTimeout); err != nil {
			// A failed auto-start is not fatal: the command can still connect on
			// its own, which is exactly what happened before daemons existed.
			return nil, false, nil
		}
		connection, err = net.DialTimeout("unix", SocketPath(), daemonDialTimeout)
		if err != nil {
			return nil, false, nil
		}
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
		Flags: []axi.Flag{
			{
				Name:    "--idle",
				Value:   "minutes",
				Desc:    "Shut down after this long without a request, 0 to stay up",
				Default: strconv.Itoa(int(defaultIdleTimeout / time.Minute)),
			},
		},
		Examples: []string{
			"discord-axi daemon start",
			"discord-axi daemon status",
			"discord-axi daemon stop",
		},
		Run: func(inv *axi.Invocation) (*axi.Doc, error) {
			idle, err := idleTimeout(inv)
			if err != nil {
				return nil, err
			}
			switch inv.Arg(0) {
			case "start":
				return startDaemonCommand(idle)
			case "run":
				return runDaemon(idle)
			case "status":
				return daemonStatus()
			case "stop":
				return stopDaemon()
			default:
				return nil, axi.Usage(`unknown daemon action "`+inv.Arg(0)+`"`,
					"Run `"+axi.Binary()+" daemon start` to start one in the background",
					"Run `"+axi.Binary()+" daemon run` to hold it in the foreground instead",
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

func runDaemon(idle time.Duration) (*axi.Doc, error) {
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

	// Progress belongs on stderr; stdout stays a single structured document.
	fmt.Fprintln(os.Stderr, "discord-axi daemon listening on "+SocketPath()+", "+idleDescription(idle))

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	uptime, reason := serveDaemon(listener, idle, signals)
	return axi.NewDoc().
		Set("daemon", "stopped after "+uptime.Round(time.Second).String()).
		Set("reason", reason), nil
}

// serveDaemon owns the accept loop, the idle clock and shutdown. It knows
// nothing about Discord, so its lifecycle can be tested without an account.
func serveDaemon(listener net.Listener, idle time.Duration, signals <-chan os.Signal) (time.Duration, string) {
	started := time.Now()
	stop := make(chan struct{})
	reasons := make(chan string, 1)

	var idleTimer *time.Timer
	if idle > 0 {
		idleTimer = time.AfterFunc(idle, func() {
			select {
			case reasons <- "idle for " + idle.String():
			default:
			}
			listener.Close()
		})
		defer idleTimer.Stop()
	}

	go func() {
		select {
		case <-signals:
			select {
			case reasons <- "signalled":
			default:
			}
		case <-stop:
		}
		listener.Close()
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			break
		}
		// Any request means someone is still using this session.
		if idleTimer != nil {
			idleTimer.Reset(idle)
		}
		if serveDaemonConnection(connection, started, stop) {
			select {
			case reasons <- "asked to stop":
			default:
			}
			break
		}
	}

	select {
	case reason := <-reasons:
		return time.Since(started), reason
	default:
		return time.Since(started), "listener closed"
	}
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
