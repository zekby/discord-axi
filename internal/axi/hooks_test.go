package axi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodexHooksEnabled(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantChanged bool
		wantContain string
	}{
		{"empty file", "", true, "[features]\nhooks = true\n"},
		{"already enabled", "[features]\nhooks = true\n", false, "hooks = true"},
		{"disabled flag", "[features]\nhooks = false\n", true, "hooks = true"},
		{"features without flag", "[features]\nother = 1\n", true, "hooks = true"},
		{"other tables only", "[tui]\ntheme = \"dark\"\n", true, "[features]\nhooks = true"},
		{"features before another table", "[features]\nother = 1\n[tui]\ntheme = \"dark\"\n", true, "hooks = true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := codexHooksEnabled(tc.in)
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v (result: %q)", changed, tc.wantChanged, got)
			}
			if !strings.Contains(got, tc.wantContain) {
				t.Fatalf("result %q does not contain %q", got, tc.wantContain)
			}
			if strings.Count(got, "[features]") > 1 {
				t.Fatalf("duplicated the features table: %q", got)
			}
		})
	}
}

func TestCodexHooksEnabledKeepsExistingKeys(t *testing.T) {
	in := "[tui]\ntheme = \"dark\"\n\n[features]\nother = 1\n"
	got, _ := codexHooksEnabled(in)
	for _, keep := range []string{"[tui]", `theme = "dark"`, "other = 1"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("dropped %q from the config:\n%s", keep, got)
		}
	}
}

func TestSessionStartHookIsIdempotentAndRepairsPaths(t *testing.T) {
	settings := map[string]any{}

	settings, changed := applySessionStartHook(settings, "discord-axi", "/usr/local/bin/discord-axi")
	if !changed {
		t.Fatal("first install reported no change")
	}

	if _, changed = applySessionStartHook(settings, "discord-axi", "/usr/local/bin/discord-axi"); changed {
		t.Fatal("repeated install with the same path must be a no-op")
	}

	settings, changed = applySessionStartHook(settings, "discord-axi", "discord-axi")
	if !changed {
		t.Fatal("a moved executable must be repaired")
	}

	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(encoded)
	if strings.Count(rendered, "discord-axi") != 1 {
		t.Fatalf("hook was duplicated instead of repaired: %s", rendered)
	}
	if !strings.Contains(rendered, `"SessionStart"`) || !strings.Contains(rendered, `"timeout":10`) {
		t.Fatalf("hook entry is not shaped as a session-start command: %s", rendered)
	}
}

func TestSessionStartHookKeepsForeignHooks(t *testing.T) {
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{map[string]any{
				"matcher": "",
				"hooks":   []any{map[string]any{"type": "command", "command": "other-tool"}},
			}},
		},
	}
	settings, _ = applySessionStartHook(settings, "discord-axi", "discord-axi")
	encoded, _ := json.Marshal(settings)
	if !strings.Contains(string(encoded), "other-tool") {
		t.Fatalf("removed an unrelated hook: %s", encoded)
	}
}
