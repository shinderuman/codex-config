package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeOverrideFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "override.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeSettings(t *testing.T, claudeConfigDir string, env map[string]any) {
	t.Helper()
	settings := map[string]any{"env": env}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeConfigDir, "settings.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestParseClaudeEnvOverrideEmptyPath(t *testing.T) {
	ov, err := parseClaudeEnvOverride("")
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.sets) != 0 || len(ov.deletes) != 0 {
		t.Fatalf("empty path must yield empty patch: %#v", ov)
	}
}

func TestParseClaudeEnvOverrideAbsentFile(t *testing.T) {
	ov, err := parseClaudeEnvOverride(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.sets) != 0 || len(ov.deletes) != 0 {
		t.Fatalf("absent file must yield empty patch: %#v", ov)
	}
}

func TestParseClaudeEnvOverrideSetAndDelete(t *testing.T) {
	path := writeOverrideFile(t, `{"env":{"SET_KEY":"value","EMPTY":"","DEL_KEY":null}}`)
	ov, err := parseClaudeEnvOverride(path)
	if err != nil {
		t.Fatal(err)
	}
	if ov.sets["SET_KEY"] != "value" {
		t.Fatalf("SET_KEY not parsed: %#v", ov.sets)
	}
	if _, ok := ov.sets["EMPTY"]; !ok || ov.sets["EMPTY"] != "" {
		t.Fatalf("EMPTY must be a real set value, not skipped: %#v", ov.sets)
	}
	found := false
	for _, key := range ov.deletes {
		if key == "DEL_KEY" {
			found = true
		}
	}
	if !found {
		t.Fatalf("DEL_KEY must be in deletes: %#v", ov.deletes)
	}
}

func TestParseClaudeEnvOverrideFailClosed(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "broken json", content: `{broken`, want: "override JSON"},
		{name: "trailing", content: `{"env":{}} extra`, want: "override JSON"},
		{name: "unknown top-level", content: `{"other":1}`, want: "top-level key"},
		{name: "non-string value", content: `{"env":{"K":123}}`, want: "stringかnullのみ"},
		{name: "env array", content: `{"env":[1,2]}`, want: "override env"},
		{name: "top-level null", content: `null`, want: "top-level nullは許可されません"},
		{name: "env null", content: `{"env":null}`, want: "nullは許可されません"},
		{name: "top-level scalar", content: `"hello"`, want: "top-levelはobjectのみ"},
		{name: "top-level array", content: `[]`, want: "top-levelはobjectのみ"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeOverrideFile(t, test.content)
			_, err := parseClaudeEnvOverride(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadSettingEnvAppliesOverrideSet(t *testing.T) {
	claudeConfigDir := t.TempDir()
	writeSettings(t, claudeConfigDir, map[string]any{
		"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic",
	})
	override := writeOverrideFile(t, `{"env":{"ANTHROPIC_BASE_URL":"https://custom.example","EXTRA_KEY":"added"}}`)

	result, _, err := loadSettingEnv(claudeConfigDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if result["ANTHROPIC_BASE_URL"] != "https://custom.example" {
		t.Fatalf("override set must overwrite settings.json value: %#v", result)
	}
	if result["EXTRA_KEY"] != "added" {
		t.Fatalf("override must allow arbitrary set key to child: %#v", result)
	}
}

func TestLoadSettingEnvOverrideDeleteSuppressesSettingsKey(t *testing.T) {
	claudeConfigDir := t.TempDir()
	writeSettings(t, claudeConfigDir, map[string]any{
		"ANTHROPIC_BASE_URL":           "https://api.z.ai/api/anthropic",
		"ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.3",
		"API_TIMEOUT_MS":               "3000000",
	})
	override := writeOverrideFile(t, `{"env":{"ANTHROPIC_BASE_URL":null,"ANTHROPIC_DEFAULT_OPUS_MODEL":null}}`)

	result, deletes, err := loadSettingEnv(claudeConfigDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result["ANTHROPIC_BASE_URL"]; ok {
		t.Fatalf("null override must delete ANTHROPIC_BASE_URL: %#v", result)
	}
	if _, ok := result["ANTHROPIC_DEFAULT_OPUS_MODEL"]; ok {
		t.Fatalf("null override must delete OPUS model redirect: %#v", result)
	}
	if result["API_TIMEOUT_MS"] != "3000000" {
		t.Fatalf("non-deleted essential key must remain: %#v", result)
	}
	if !containsString(deletes, "ANTHROPIC_BASE_URL") || !containsString(deletes, "ANTHROPIC_DEFAULT_OPUS_MODEL") {
		t.Fatalf("tombstones must be returned for buildChildEnv deny: %#v", deletes)
	}
}

func TestLoadSettingEnvOverrideAbsentEqualsNoOverride(t *testing.T) {
	claudeConfigDir := t.TempDir()
	writeSettings(t, claudeConfigDir, map[string]any{
		"ANTHROPIC_BASE_URL":                       "https://api.z.ai/api/anthropic",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":             "glm-5.3",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
	})

	without, withoutDeletes, err := loadSettingEnv(claudeConfigDir, "")
	if err != nil {
		t.Fatal(err)
	}
	withAbsent, withAbsentDeletes, err := loadSettingEnv(claudeConfigDir, filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(without) != len(withAbsent) {
		t.Fatalf("absent override must equal no override: %#v vs %#v", without, withAbsent)
	}
	for key, value := range without {
		if withAbsent[key] != value {
			t.Fatalf("absent override diverges at %s: %#v vs %#v", key, without, withAbsent)
		}
	}
	if len(withoutDeletes) != 0 || len(withAbsentDeletes) != 0 {
		t.Fatalf("absent override must yield no tombstones: %#v / %#v", withoutDeletes, withAbsentDeletes)
	}
}

func TestLoadSettingEnvOverrideEmptyStringIsSet(t *testing.T) {
	claudeConfigDir := t.TempDir()
	writeSettings(t, claudeConfigDir, map[string]any{"API_TIMEOUT_MS": "3000000"})

	override := writeOverrideFile(t, `{"env":{"API_TIMEOUT_MS":""}}`)
	result, _, err := loadSettingEnv(claudeConfigDir, override)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := result["API_TIMEOUT_MS"]; !ok || value != "" {
		t.Fatalf("empty string override must be a real set value, not unset: %#v", result)
	}
}

func TestLoadSettingEnvOverrideMalformedFails(t *testing.T) {
	claudeConfigDir := t.TempDir()
	writeSettings(t, claudeConfigDir, map[string]any{"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"})

	override := writeOverrideFile(t, `{"env":{"K":123}}`)
	_, _, err := loadSettingEnv(claudeConfigDir, override)
	if err == nil {
		t.Fatal("malformed override must fail")
	}
}

func TestBuildChildEnvDoesNotReflowDeletedKeyFromParent(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "parent-zai-leak")
	t.Setenv("ANTHROPIC_DEFAULT_OPUS_MODEL", "parent-opus-leak")

	result := buildChildEnv(nil, map[string]string{"ANTHROPIC_AUTH_TOKEN": "token"}, nil, nil)
	joined := strings.Join(result, "\n")
	if strings.Contains(joined, "parent-zai-leak") || strings.Contains(joined, "parent-opus-leak") {
		t.Fatalf("deleted/absent key must not reflow from parent: %#v", result)
	}
	if !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=token") {
		t.Fatalf("settingEnv key must be present: %#v", result)
	}
}

// 親が値を持ちEnvAllowlistにも含まれるkeyをoverride nullにした場合、
// tombstoneがextra-allow経由の親env再流入を遮断するか検証する。
func TestBuildChildEnvTombstoneBlocksExtraAllowReflow(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "parent-zai-leak")

	result := buildChildEnv(
		[]string{"ANTHROPIC_BASE_URL"},
		map[string]string{"ANTHROPIC_AUTH_TOKEN": "token"},
		nil,
		[]string{"ANTHROPIC_BASE_URL"},
	)
	joined := strings.Join(result, "\n")
	if strings.Contains(joined, "parent-zai-leak") {
		t.Fatalf("tombstone must block extra-allow reflow from parent: %#v", result)
	}
	if strings.Contains(joined, "ANTHROPIC_BASE_URL=") {
		t.Fatalf("tombstoned key must be absent from child env: %#v", result)
	}
	if !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=token") {
		t.Fatalf("non-tombstoned settingEnv key must remain: %#v", result)
	}
}

// 親が値を持ちEnvAllowlistにも含まれるkeyをoverride setにした場合、
// override値が親より優先されるか検証する。
func TestBuildChildEnvOverrideSetWinsOverParent(t *testing.T) {
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "parent-token")

	result := buildChildEnv(
		[]string{"ANTHROPIC_AUTH_TOKEN"},
		map[string]string{"ANTHROPIC_AUTH_TOKEN": "override-token"},
		nil,
		nil,
	)
	joined := strings.Join(result, "\n")
	if strings.Contains(joined, "parent-token") {
		t.Fatalf("override set must win over parent: %#v", result)
	}
	if !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=override-token") {
		t.Fatalf("override set value must be present: %#v", result)
	}
}
