package app

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"

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
	fakeDaemon(t, daemonResponse{Output: "error: token rejected\n", Code: axi.ExitError})

	_, served, err := forwardToDaemon("unread", nil)
	if !served || err == nil {
		t.Fatalf("served = %v, err = %v, want a relayed failure", served, err)
	}
	if !strings.Contains(err.Error(), "token rejected") {
		t.Fatalf("err = %v, want the daemon's message", err)
	}
}

func TestCommandsFallBackWhenNoDaemonIsListening(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", shortTempDir(t))

	doc, served, err := forwardToDaemon("unread", nil)
	if served || doc != nil || err != nil {
		t.Fatalf("served = %v, doc = %v, err = %v, want a clean fall-through", served, doc, err)
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
