package app

import (
	"os"
	"path/filepath"
)

// StateDir holds everything that must survive between invocations: the request
// pacer's clock, the cached client build number and the daemon socket.
func StateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(os.TempDir(), "discord-axi")
		}
		base = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(base, "discord-axi")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// maxSocketPath is the shortest sun_path limit across the platforms this runs
// on (macOS allows 104 bytes, Linux 108); a longer path fails to bind with an
// error that says nothing useful.
const maxSocketPath = 100

// SocketPath is where the daemon listens.
func SocketPath() string {
	if runtime := os.Getenv("XDG_RUNTIME_DIR"); runtime != "" {
		if path := filepath.Join(runtime, "discord-axi.sock"); len(path) <= maxSocketPath {
			return path
		}
	}
	if path := filepath.Join(StateDir(), "daemon.sock"); len(path) <= maxSocketPath {
		return path
	}
	return filepath.Join(os.TempDir(), "discord-axi.sock")
}
