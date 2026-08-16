package axi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// hookTimeoutSeconds bounds how long an agent waits for the ambient home view.
const hookTimeoutSeconds = 10

// HookInstall reports what a setup run did to one integration.
type HookInstall struct {
	App     string
	Path    string
	Changed bool
	Err     error
}

// InstallSessionStartHooks registers the home view as session-start context for
// Claude Code, Codex and OpenCode. It is idempotent: an entry that already
// points at this executable is left alone, and one that points elsewhere is
// repaired.
func InstallSessionStartHooks(marker string) []HookInstall {
	home, err := os.UserHomeDir()
	if err != nil {
		return []HookInstall{{App: "all", Err: err}}
	}
	command := portableCommand(marker)

	return []HookInstall{
		installJSONHook("Claude Code", filepath.Join(home, ".claude", "settings.json"), marker, command),
		installJSONHook("Codex", filepath.Join(home, ".codex", "hooks.json"), marker, command),
		enableCodexHooks(filepath.Join(home, ".codex", "config.toml")),
		installOpenCodePlugin(home, marker, command),
	}
}

// portableCommand prefers the bare binary name, but only when PATH resolves it
// to this very executable; otherwise a global install could shadow it.
func portableCommand(marker string) string {
	execPath := ExecPath()
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		candidate, err := filepath.EvalSymlinks(filepath.Join(dir, marker))
		if err == nil && candidate == execPath {
			return marker
		}
	}
	return execPath
}

func installJSONHook(app, path, marker, command string) HookInstall {
	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return HookInstall{App: app, Path: path, Err: err}
		}
	}

	updated, changed := applySessionStartHook(settings, marker, command)
	if !changed {
		return HookInstall{App: app, Path: path}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return HookInstall{App: app, Path: path, Err: err}
	}
	encoded, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return HookInstall{App: app, Path: path, Err: err}
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		return HookInstall{App: app, Path: path, Err: err}
	}
	return HookInstall{App: app, Path: path, Changed: true}
}

// applySessionStartHook edits the settings tree in place and reports whether
// anything actually changed, so a repeated install stays a silent no-op.
func applySessionStartHook(settings map[string]any, marker, command string) (map[string]any, bool) {
	changed := false

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
		changed = true
	}

	groups, _ := hooks["SessionStart"].([]any)
	if hooks["SessionStart"] == nil {
		groups = []any{}
		changed = true
	}

	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		entries, _ := group["hooks"].([]any)
		for _, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				continue
			}
			existing, _ := entry["command"].(string)
			if !strings.Contains(existing, marker) {
				continue
			}
			if existing == command && entry["type"] == "command" && asInt(entry["timeout"]) == hookTimeoutSeconds {
				hooks["SessionStart"] = groups
				return settings, changed
			}
			entry["command"] = command
			entry["type"] = "command"
			entry["timeout"] = hookTimeoutSeconds
			hooks["SessionStart"] = groups
			return settings, true
		}
	}

	hooks["SessionStart"] = append(groups, map[string]any{
		"matcher": "",
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": command,
			"timeout": hookTimeoutSeconds,
		}},
	})
	return settings, true
}

// asInt reads a number that may come from freshly written Go values or from a
// JSON round trip, where every number is a float64.
func asInt(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	default:
		return 0
	}
}

func enableCodexHooks(path string) HookInstall {
	current := ""
	if raw, err := os.ReadFile(path); err == nil {
		current = string(raw)
	}
	updated, changed := codexHooksEnabled(current)
	if !changed {
		return HookInstall{App: "Codex config", Path: path}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return HookInstall{App: "Codex config", Path: path, Err: err}
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return HookInstall{App: "Codex config", Path: path, Err: err}
	}
	return HookInstall{App: "Codex config", Path: path, Changed: true}
}

var (
	tomlSection  = regexp.MustCompile(`^\s*\[{1,2}([^\]]+)\]{1,2}\s*(?:#.*)?$`)
	tomlHooksKey = regexp.MustCompile(`^\s*hooks\s*=\s*(true|false)\s*(?:#.*)?$`)
)

// codexHooksEnabled sets [features].hooks = true without disturbing the rest of
// the user's config.
func codexHooksEnabled(content string) (string, bool) {
	if strings.TrimSpace(content) == "" {
		return "[features]\nhooks = true\n", true
	}

	lines := strings.Split(content, "\n")
	inFeatures := false
	sawFeatures := false
	for index, line := range lines {
		if match := tomlSection.FindStringSubmatch(line); match != nil {
			if inFeatures {
				updated := append([]string{}, lines[:index]...)
				updated = append(updated, "hooks = true")
				updated = append(updated, lines[index:]...)
				return strings.Join(updated, "\n"), true
			}
			inFeatures = strings.TrimSpace(match[1]) == "features"
			sawFeatures = sawFeatures || inFeatures
			continue
		}
		if !inFeatures {
			continue
		}
		if flag := tomlHooksKey.FindStringSubmatch(line); flag != nil {
			if flag[1] == "true" {
				return content, false
			}
			lines[index] = strings.Replace(line, "false", "true", 1)
			return strings.Join(lines, "\n"), true
		}
	}

	suffix := "\n"
	if strings.HasSuffix(content, "\n") {
		suffix = ""
	}
	if sawFeatures {
		return content + suffix + "hooks = true\n", true
	}
	return content + suffix + "\n[features]\nhooks = true\n", true
}

const openCodeManagedMarker = "discord-axi managed opencode plugin:"

func installOpenCodePlugin(home, marker, command string) HookInstall {
	path := filepath.Join(home, ".config", "opencode", "plugins", "axi-"+marker+".js")
	next := openCodePluginSource(marker, command)

	if current, err := os.ReadFile(path); err == nil {
		if !strings.Contains(string(current), openCodeManagedMarker+" "+marker) {
			return HookInstall{App: "OpenCode", Path: path,
				Err: errUnmanagedPlugin}
		}
		if string(current) == next {
			return HookInstall{App: "OpenCode", Path: path}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return HookInstall{App: "OpenCode", Path: path, Err: err}
	}
	if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
		return HookInstall{App: "OpenCode", Path: path, Err: err}
	}
	return HookInstall{App: "OpenCode", Path: path, Changed: true}
}

var errUnmanagedPlugin = &Error{
	Message: "refusing to overwrite an OpenCode plugin this CLI did not write",
	Code:    "PLUGIN_CONFLICT",
}

// openCodePluginSource injects the home view as ambient model context, which is
// OpenCode's equivalent of a session-start hook.
func openCodePluginSource(marker, command string) string {
	quotedCommand, _ := json.Marshal(command)
	quotedMarker, _ := json.Marshal(marker)
	return `// ` + openCodeManagedMarker + ` ` + marker + `
// Generated by ` + marker + ` setup hooks. Remove the marker above to keep local edits.
import { spawn } from "node:child_process";

const command = ` + string(quotedCommand) + `;
const marker = ` + string(quotedMarker) + `;
const timeoutMs = ` + strconv.Itoa(hookTimeoutSeconds*1000) + `;

function runHomeView(directory) {
  return new Promise((resolve) => {
    const child = spawn(command, [], {
      cwd: directory || process.cwd(),
      env: process.env,
      stdio: ["ignore", "pipe", "pipe"],
    });

    let stdout = "";
    let settled = false;
    const finish = (value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(value);
    };
    const timer = setTimeout(() => {
      child.kill("SIGTERM");
      finish("");
    }, timeoutMs);

    child.stdout.setEncoding("utf-8");
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.on("error", () => finish(""));
    child.on("close", (code) => finish(code === 0 ? stdout.trim() : ""));
  });
}

export const AxiDiscordAmbientContextPlugin = async ({ directory }) => {
  const cache = new Map();
  return {
    "experimental.chat.system.transform": async (input, output) => {
      const key = input.sessionID ?? "__global__";
      if (!cache.has(key)) {
        cache.set(key, await runHomeView(directory));
      }
      const homeView = cache.get(key);
      if (homeView) {
        output.system.push("## AXI ambient context: " + marker + "\n" + homeView);
      }
    },
  };
};
`
}
