package app

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
)

// A real client's requests are spread out by a human clicking. An agent's are
// not, and a burst is the clearest self-bot signal there is. The pacer enforces
// a minimum gap between requests across every discord-axi process on the
// machine, because an agent can easily run twenty of them at once.
const (
	defaultMinInterval = 1200 * time.Millisecond
	// jitterFraction keeps the gap from being a metronome.
	jitterFraction = 0.4
	// maxPacerWait bounds how long one request may be held back.
	maxPacerWait = 15 * time.Second
	// MinIntervalEnvVar overrides the gap, in milliseconds. 0 disables pacing.
	MinIntervalEnvVar = "DISCORD_AXI_MIN_INTERVAL_MS"
)

func minInterval() time.Duration {
	raw, ok := os.LookupEnv(MinIntervalEnvVar)
	if !ok {
		return defaultMinInterval
	}
	milliseconds, err := strconv.Atoi(raw)
	if err != nil || milliseconds < 0 {
		return defaultMinInterval
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func pacePath() string { return filepath.Join(StateDir(), "pace") }

// pace blocks until enough time has passed since the last request made by any
// discord-axi process, then records this request as the new last one.
func pace() {
	interval := minInterval()
	if interval == 0 {
		return
	}
	if wait := reserveSlot(pacePath(), interval); wait > 0 {
		time.Sleep(wait)
	}
}

// reserveSlot claims the next send slot under an exclusive lock and returns how
// long the caller must wait for it. The lock is held only for the bookkeeping,
// never for the sleep, so concurrent processes queue up instead of colliding.
func reserveSlot(path string, interval time.Duration) time.Duration {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return 0
	}
	defer file.Close()

	unlock, err := lockFile(file)
	if err != nil {
		return 0
	}
	defer unlock()

	now := time.Now()
	next := now
	if previous := readSlot(file); !previous.IsZero() {
		if earliest := previous.Add(interval + jitter(interval)); earliest.After(now) {
			next = earliest
		}
	}
	writeSlot(file, next)

	wait := next.Sub(now)
	if wait > maxPacerWait {
		return maxPacerWait
	}
	return wait
}

func jitter(interval time.Duration) time.Duration {
	return time.Duration(rand.Float64() * jitterFraction * float64(interval))
}

func readSlot(file *os.File) time.Time {
	buffer := make([]byte, 32)
	read, err := file.ReadAt(buffer, 0)
	if read == 0 && err != nil {
		return time.Time{}
	}
	nanoseconds, err := strconv.ParseInt(string(trimNumber(buffer[:read])), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(0, nanoseconds)
}

func writeSlot(file *os.File, when time.Time) {
	_ = file.Truncate(0)
	_, _ = file.WriteAt([]byte(strconv.FormatInt(when.UnixNano(), 10)), 0)
}

func trimNumber(raw []byte) []byte {
	end := 0
	for end < len(raw) && raw[end] >= '0' && raw[end] <= '9' {
		end++
	}
	return raw[:end]
}

// pacedRequest is the arikawa hook that applies the pacer to every REST call.
func pacedRequest(httpdriver.Request) error {
	pace()
	return nil
}
