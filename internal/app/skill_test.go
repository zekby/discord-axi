package app

import (
	"os"
	"strings"
	"testing"
)

// The committed skill must stay in step with the command table it is generated
// from, so a new command can never ship with stale agent-facing docs.
func TestCommittedSkillIsUpToDate(t *testing.T) {
	committed, err := os.ReadFile("../../SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(committed) != SkillMarkdown() {
		t.Fatal("SKILL.md is stale; run `discord-axi setup skill --path SKILL.md`")
	}
}

// An agent must be able to tell a read from a write before it runs one, so the
// warning has to exist in all three places it can look: the skill, the command
// table and the per-command help.
func TestEveryWriteCommandDeclaresItsBanRisk(t *testing.T) {
	for _, command := range Commands() {
		help := command.Help().Encode()
		if IsWrite(command.Name) {
			if command.Caution != WriteCaution {
				t.Errorf("`%s` writes but carries no caution", command.Name)
			}
			if !strings.Contains(help, "caution:") || !strings.Contains(help, "account being disabled") {
				t.Errorf("`%s --help` does not state the risk:\n%s", command.Name, help)
			}
			continue
		}
		if strings.Contains(help, "caution:") {
			t.Errorf("`%s` only reads but warns like a write:\n%s", command.Name, help)
		}
	}
}

func TestSkillSeparatesReadsFromWrites(t *testing.T) {
	skill := SkillMarkdown()
	for _, want := range []string{
		"## Ban risk",
		"effect: write",
		"`send`, `edit`, `delete`, `react`, `read`",
		"discordo/issues/813",
		"kind: bot",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("skill is missing %q", want)
		}
	}
	for _, command := range Commands() {
		if !IsWrite(command.Name) {
			continue
		}
		if !strings.Contains(skill, command.Name+",write,") {
			t.Errorf("`%s` is not marked as a write in the skill's command table", command.Name)
		}
	}
}

func TestSkillCarriesNoLiveState(t *testing.T) {
	skill := SkillMarkdown()
	for _, forbidden := range []string{"account:", "guilds[", "unread["} {
		if strings.Contains(skill, forbidden) {
			t.Fatalf("skill must stay static, found %q", forbidden)
		}
	}
}
