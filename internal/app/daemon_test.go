package app

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zekby/discord-axi/internal/axi"
)

// shortTempDir keeps socket paths under the sun_path limit, which the standard
// test temp directory would blow past.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "axi")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// fakeDaemon answers one request the way the real daemon would.
func fakeDaemon(t *testing.T, response daemonResponse) chan daemonRequest {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t))

	listener, err := net.Listen("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	received := make(chan daemonRequest, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		var request daemonRequest
		if err := json.NewDecoder(connection).Decode(&request); err != nil {
			return
		}
		received <- request
		_ = json.NewEncoder(connection).Encode(response)
	}()
	return received
}

func TestForwardToDaemonRelaysCommandAndOutput(t *testing.T) {
	received := fakeDaemon(t, daemonResponse{Output: "unread: 0 unread channels\n", Code: axi.ExitOK})

	doc, served, err := forwardToDaemon("unread", []string{"--mentions"})
	if err != nil || !served {
		t.Fatalf("served = %v, err = %v", served, err)
	}
	if got := doc.Encode(); got != "unread: 0 unread channels" {
		t.Fatalf("output = %q, want the daemon's own TOON", got)
	}

	request := <-received
	if request.Command != "unread" || len(request.Args) != 1 || request.Args[0] != "--mentions" {
		t.Fatalf("daemon received %+v, want the original argv", request)
	}
}

func TestForwardToDaemonSurfacesDaemonErrors(t *testing.T) {
	fakeDaemon(t, daemonResponse{Output: "error: token rejected\ncode: NOT_AUTHENTICATED\n", Code: axi.ExitError})

	_, served, err := forwardToDaemon("unread", nil)
	if !served || err == nil {
		t.Fatalf("served = %v, err = %v, want a relayed failure", served, err)
	}
	if got := axi.ErrorDoc(err).Encode(); got != "error: token rejected\ncode: NOT_AUTHENTICATED" {
		t.Fatalf("output = %q, want the daemon's own document", got)
	}
	if code := axi.ExitCode(err); code != axi.ExitError {
		t.Fatalf("exit code = %d, want the daemon's own", code)
	}
}

// A daemon failure is already a structured document. Wrapping it again would
// nest one error inside the message of another, which an agent cannot read.
func TestRelayedUsageErrorKeepsItsCodeAndExitStatus(t *testing.T) {
	var rendered strings.Builder
	code := App("daemon").Run([]string{"guilds", "--nope"}, &rendered)
	fakeDaemon(t, daemonResponse{Output: rendered.String(), Code: code})

	_, served, err := forwardToDaemon("unread", nil)
	if !served || err == nil {
		t.Fatalf("served = %v, err = %v, want a relayed failure", served, err)
	}
	relayed := axi.ErrorDoc(err).Encode()
	if strings.Count(relayed, "code:") != 1 || !strings.Contains(relayed, axi.CodeValidation) {
		t.Fatalf("the daemon error was re-wrapped instead of relayed:\n%s", relayed)
	}
	if got := axi.ExitCode(err); got != axi.ExitUsage {
		t.Fatalf("exit code = %d, want %d", got, axi.ExitUsage)
	}
}

func TestCommandsFallBackWhenAutoStartIsRefused(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t))
	t.Setenv(NoDaemonEnvVar, "1")

	doc, served, err := forwardToDaemon("unread", nil)
	if served || doc != nil || err != nil {
		t.Fatalf("served = %v, doc = %v, err = %v, want a clean fall-through", served, doc, err)
	}
	if autoStartEnabled() {
		t.Fatal(NoDaemonEnvVar + " must switch auto-start off")
	}
}

func TestAutoStartGivesUpQuietlyWithoutCredentials(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(TokenEnvVar, "")
	t.Setenv("PATH", "") // keeps the keyring helper out of the test

	start := time.Now()
	_, served, err := forwardToDaemon("unread", nil)
	if served || err != nil {
		t.Fatalf("served = %v, err = %v, want a fall-through the command can recover from", served, err)
	}
	// It must refuse before spawning anything, not wait out the startup window.
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s to decline; it should never have spawned a child", elapsed)
	}
}

func TestIdleFlagParsing(t *testing.T) {
	command := daemonCommand()

	inv, err := command.Parse([]string{"start"})
	if err != nil {
		t.Fatal(err)
	}
	if idle, err := idleTimeout(inv); err != nil || idle != defaultIdleTimeout {
		t.Fatalf("default idle = %s, err = %v, want %s", idle, err, defaultIdleTimeout)
	}

	inv, _ = command.Parse([]string{"start", "--idle", "0"})
	if idle, err := idleTimeout(inv); err != nil || idle != 0 {
		t.Fatalf("idle = %s, err = %v, want 0 to mean stay up", idle, err)
	}

	inv, _ = command.Parse([]string{"start", "--idle", "soon"})
	if _, err := idleTimeout(inv); err == nil || axi.ExitCode(err) != axi.ExitUsage {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestOnlyGatewayCommandsAreRelayed(t *testing.T) {
	fakeDaemon(t, daemonResponse{Output: "unused", Code: axi.ExitOK})

	if _, served, _ := forwardToDaemon("messages", []string{"general"}); served {
		t.Fatal("plain REST commands must not be relayed through the daemon")
	}
}

func TestDaemonStatusReportsWhenNothingIsRunning(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t))

	doc, err := daemonStatus()
	if err != nil {
		t.Fatal(err)
	}
	rendered := doc.Encode()
	if !strings.Contains(rendered, "daemon: not running") || !strings.Contains(rendered, "daemon run") {
		t.Fatalf("status should state the zero and suggest starting it:\n%s", rendered)
	}
}

func TestStopIsANoOpWhenNothingIsRunning(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t))

	doc, err := stopDaemon()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc.Encode(), "(no-op)") {
		t.Fatalf("stopping nothing must be a no-op:\n%s", doc.Encode())
	}
}

// listenForTest gives serveDaemon a socket without needing Discord.
func listenForTest(t *testing.T) net.Listener {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t))
	listener, err := net.Listen("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	return listener
}

func TestDaemonShutsDownWhenLeftIdle(t *testing.T) {
	listener := listenForTest(t)

	done := make(chan string, 1)
	go func() {
		_, reason := serveDaemon(listener, 300*time.Millisecond, nil)
		done <- reason
	}()

	select {
	case reason := <-done:
		if !strings.Contains(reason, "idle") {
			t.Fatalf("reason = %q, want the idle timeout", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an idle daemon must shut itself down")
	}
}

func TestEachRequestResetsTheIdleClock(t *testing.T) {
	listener := listenForTest(t)

	const idle = 600 * time.Millisecond
	started := time.Now()
	done := make(chan time.Duration, 1)
	go func() {
		uptime, _ := serveDaemon(listener, idle, nil)
		done <- uptime
	}()

	time.Sleep(400 * time.Millisecond)
	connection, err := net.Dial("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewEncoder(connection).Encode(daemonRequest{Command: "__ping"})
	var response daemonResponse
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatal(err)
	}
	connection.Close()
	if !strings.Contains(response.Output, "running") {
		t.Fatalf("ping answered %q", response.Output)
	}

	select {
	case uptime := <-done:
		if uptime < 900*time.Millisecond {
			t.Fatalf("shut down after %s; the request at 400ms should have pushed it past %s",
				uptime, 400*time.Millisecond+idle)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("did not shut down at all, %s after start", time.Since(started))
	}
}

func TestDaemonStopsOnRequest(t *testing.T) {
	listener := listenForTest(t)

	done := make(chan string, 1)
	go func() {
		_, reason := serveDaemon(listener, 0, nil)
		done <- reason
	}()

	connection, err := net.Dial("unix", SocketPath())
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewEncoder(connection).Encode(daemonRequest{Command: "__stop"})
	var response daemonResponse
	_ = json.NewDecoder(connection).Decode(&response)
	connection.Close()

	select {
	case reason := <-done:
		if reason != "asked to stop" {
			t.Fatalf("reason = %q", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("`daemon stop` must end the accept loop")
	}
}

// Two setups whose paths are too long for a socket must not fall back onto one
// shared name: that would hand one account's live session to the other.
func TestSocketFallbackIsNotSharedBetweenStateDirectories(t *testing.T) {
	deep := strings.Repeat("/nested", 20)

	t.Setenv("XDG_RUNTIME_DIR", deep)
	t.Setenv("XDG_STATE_HOME", os.TempDir()+"/a"+deep)
	first := SocketPath()
	t.Setenv("XDG_STATE_HOME", os.TempDir()+"/b"+deep)
	second := SocketPath()

	if first == second {
		t.Fatalf("both state directories fell back to %s", first)
	}
	for _, path := range []string{first, second} {
		if len(path) > maxSocketPath {
			t.Fatalf("fallback socket %s is %d bytes, over the %d limit", path, len(path), maxSocketPath)
		}
	}
}
