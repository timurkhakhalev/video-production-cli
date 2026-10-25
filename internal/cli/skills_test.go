package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSkillsUsage(t *testing.T) {
	out, errs, code := runTestCLI(t, "skills")
	if code != 0 || errs != "" {
		t.Fatalf("skills: %d %s", code, errs)
	}
	for _, want := range []string{"core", "cutting", "skills list", "skills get core cutting", "skills get core --full", "skills path core", "skills install --agent gemini"} {
		if !strings.Contains(out, want) {
			t.Errorf("skills output lacks %q: %s", want, out)
		}
	}
	out, errs, code = runTestCLI(t, "skills", "core", "cutting")
	if code == 0 || out != "" || !strings.Contains(errs, "video-production-cli skills get core cutting") {
		t.Fatalf("invalid invocation: %d %q %q", code, out, errs)
	}
	out, errs, code = runTestCLI(t, "skills", "get", "core", "cutting")
	if code != 0 || errs != "" || !strings.Contains(out, "## Workflow") || !strings.Contains(out, "## What to cut") {
		t.Fatalf("get sections: %d %s %s", code, out, errs)
	}
	out, errs, code = runTestCLI(t, "skills", "--json")
	if code != 0 || errs != "" || !json.Valid([]byte(out)) {
		t.Fatalf("JSON: %d %s %s", code, out, errs)
	}
	out, errs, code = runTestCLI(t, "skills", "list")
	if code != 0 || errs != "" || strings.Contains(out, "Examples:") {
		t.Fatalf("list: %d %s %s", code, out, errs)
	}
}
