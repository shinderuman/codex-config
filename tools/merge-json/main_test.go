package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeFilesPreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")

	if err := os.WriteFile(target, []byte(`{"permissions":{"allow":["x"]},"env":{"LOCAL":"keep"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragment, []byte(`{"env":{"MANAGED":"yes"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := mergeFiles(target, fragment, "")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected change")
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)

	for _, want := range []string{`"permissions"`, `"LOCAL": "keep"`, `"MANAGED": "yes"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}

func TestMergeFilesIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")

	if err := os.WriteFile(target, []byte(`{"env":{"A":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragment, []byte(`{"env":{"A":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := mergeFiles(target, fragment, "")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("expected unchanged")
	}
}

func TestMergeFilesCreatesMissingTargetWithPrivateMode(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "nested", "settings.json")
	fragment := filepath.Join(directory, "managed.json")
	if err := os.WriteFile(fragment, []byte(`{"env":{"MANAGED":"yes"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	changed, err := mergeFiles(target, fragment, "")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("missing targetの作成を変更として返す必要があります")
	}
	stat, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("target mode = %o", stat.Mode().Perm())
	}
}

func TestMergeFilesPreservesTargetMode(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "settings.json")
	fragment := filepath.Join(directory, "managed.json")
	if err := os.WriteFile(target, []byte(`{"local":true}`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fragment, []byte(`{"managed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := mergeFiles(target, fragment, ""); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o640 {
		t.Fatalf("target mode = %o", stat.Mode().Perm())
	}
}

func TestMergeFilesRejectsInvalidJSONShapes(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		fragment string
		want     string
	}{
		{name: "target syntax", target: `{broken`, fragment: `{}`, want: "target JSON"},
		{name: "fragment syntax", target: `{}`, fragment: `{broken`, want: "fragment JSON"},
		{name: "target array", target: `[]`, fragment: `{}`, want: "cannot unmarshal array"},
		{name: "fragment array", target: `{}`, fragment: `[]`, want: "cannot unmarshal array"},
		{name: "multiple values", target: `{}`, fragment: `{} {}`, want: "multiple JSON values"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "settings.json")
			fragment := filepath.Join(directory, "managed.json")
			if err := os.WriteFile(target, []byte(test.target), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fragment, []byte(test.fragment), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := mergeFiles(target, fragment, "")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMergeFilesAppliesEnvOverrideSet(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, target, `{"env":{"EXISTING":"keep"}}`)
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, override, `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com","EXISTING":"overwritten"}}`)

	changed, err := mergeFiles(target, fragment, override)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected change")
	}
	text := string(mustRead(t, target))
	for _, want := range []string{
		`"MANAGED": "yes"`,
		`"ANTHROPIC_BASE_URL": "https://api.anthropic.com"`,
		`"EXISTING": "overwritten"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}

func TestMergeFilesEnvOverrideNullDeletesKey(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
	writeFile(t, fragment, `{"env":{"ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic","MANAGED":"yes"}}`)
	writeFile(t, override, `{"env":{"ANTHROPIC_BASE_URL":null}}`)

	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	text := string(mustRead(t, target))
	if strings.Contains(text, "z.ai") {
		t.Fatalf("null override should delete ANTHROPIC_BASE_URL: %s", text)
	}
	for _, want := range []string{`"MANAGED": "yes"`, `"LOCAL": "keep"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
}

func TestMergeFilesOverrideNoOpVariantsEqualNoOverride(t *testing.T) {
	fragmentDir := t.TempDir()
	fragment := filepath.Join(fragmentDir, "managed.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)

	noOverrideDir := t.TempDir()
	noOverrideTarget := filepath.Join(noOverrideDir, "settings.json")
	writeFile(t, noOverrideTarget, `{"env":{"LOCAL":"keep"}}`)
	if _, err := mergeFiles(noOverrideTarget, fragment, ""); err != nil {
		t.Fatal(err)
	}
	baseline := string(mustRead(t, noOverrideTarget))

	for _, name := range []string{"absent", "empty object", "empty env"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "settings.json")
			writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
			override := filepath.Join(dir, "override.json")
			switch name {
			case "absent":
				override = filepath.Join(dir, "absent.json")
			case "empty object":
				writeFile(t, override, `{}`)
			case "empty env":
				writeFile(t, override, `{"env":{}}`)
			}
			if _, err := mergeFiles(target, fragment, override); err != nil {
				t.Fatal(err)
			}
			got := string(mustRead(t, target))
			if got != baseline {
				t.Fatalf("no-op override variant %q must equal no override:\n%s\n%s", name, baseline, got)
			}
		})
	}
}

func TestMergeFilesOverrideIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, override, `{"env":{"ANTHROPIC_BASE_URL":null,"EXTRA":"set"}}`)

	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	first := string(mustRead(t, target))

	changed, err := mergeFiles(target, fragment, override)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("second merge should report unchanged (idempotent)")
	}
	second := string(mustRead(t, target))
	if first != second {
		t.Fatalf("idempotency broken:\n%s\n%s", first, second)
	}
}

func TestMergeFilesOverrideRejectsInvalid(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "broken json", content: `{broken`, want: "override JSON"},
		{name: "trailing", content: `{"env":{}} extra`, want: "override JSON"},
		{name: "unknown top-level", content: `{"foo":1}`, want: "top-level key"},
		{name: "non-string value", content: `{"env":{"K":123}}`, want: "stringかnullのみ"},
		{name: "env array", content: `{"env":[1,2]}`, want: "override env"},
		{name: "top-level null", content: `null`, want: "top-level nullは許可されません"},
		{name: "env null", content: `{"env":null}`, want: "nullは許可されません"},
		{name: "top-level scalar", content: `"hello"`, want: "top-levelはobjectのみ"},
		{name: "top-level array", content: `[]`, want: "top-levelはobjectのみ"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "settings.json")
			fragment := filepath.Join(dir, "managed.json")
			override := filepath.Join(dir, "override.json")
			writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
			writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
			writeFile(t, override, test.content)

			_, err := mergeFiles(target, fragment, override)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			text := string(mustRead(t, target))
			if strings.Contains(text, "MANAGED") {
				t.Fatalf("fail-closed violated: target was written: %s", text)
			}
		})
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestMergeFilesOverrideAddedKeyRemovedFromOverride(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
	writeFile(t, override, `{"env":{"EXTRA":"added"}}`)

	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, target)), `"EXTRA": "added"`) {
		t.Fatalf("first install must apply EXTRA: %s", mustRead(t, target))
	}

	// overrideからEXTRAを外すと空patchになり、EXTRAは不存在へ復元される。
	writeFile(t, override, `{}`)
	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	text := string(mustRead(t, target))
	if strings.Contains(text, "EXTRA") {
		t.Fatalf("EXTRA must be reverted to absent: %s", text)
	}
	for _, want := range []string{`"MANAGED": "yes"`, `"LOCAL": "keep"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	state, err := loadOverrideState(statePathFor(target))
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Env) != 0 {
		t.Fatalf("empty override must yield empty state: %#v", state.Env)
	}
}

func TestMergeFilesOverrideOverwriteRestoresOriginal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, target, `{"env":{"LOCAL":"original"}}`)
	writeFile(t, override, `{"env":{"LOCAL":"overwritten"}}`)

	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, target)), `"LOCAL": "overwritten"`) {
		t.Fatalf("override must overwrite LOCAL: %s", mustRead(t, target))
	}
	state, err := loadOverrideState(statePathFor(target))
	if err != nil {
		t.Fatal(err)
	}
	if base, ok := state.Env["LOCAL"]; !ok || !base.Exists || base.Value != "original" {
		t.Fatalf("state must record LOCAL baseline original: %#v", state.Env["LOCAL"])
	}

	// override削除(空patch)でLOCALは元値originalへ復元される。
	writeFile(t, override, `{}`)
	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, target)), `"LOCAL": "original"`) {
		t.Fatalf("LOCAL must be restored to original: %s", mustRead(t, target))
	}
}

func TestMergeFilesOverrideNullRestoresExistingKey(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, target, `{"env":{"LOCAL":"original"}}`)
	writeFile(t, override, `{"env":{"LOCAL":null}}`)

	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustRead(t, target)), "LOCAL") {
		t.Fatalf("null override must delete LOCAL: %s", mustRead(t, target))
	}

	// override削除でLOCALは元値originalへ復元される。
	writeFile(t, override, `{}`)
	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, target)), `"LOCAL": "original"`) {
		t.Fatalf("deleted LOCAL must be restored to original: %s", mustRead(t, target))
	}
}

func TestMergeFilesOverrideManagedKeyRestoredToDefault(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic","MANAGED":"yes"}}`)
	writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
	writeFile(t, override, `{"env":{"ANTHROPIC_BASE_URL":null}}`)

	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mustRead(t, target)), "z.ai") {
		t.Fatalf("null override must delete managed ANTHROPIC_BASE_URL: %s", mustRead(t, target))
	}

	// override削除でmanaged defaultへ復元される。
	writeFile(t, override, `{}`)
	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mustRead(t, target)), `"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"`) {
		t.Fatalf("managed ANTHROPIC_BASE_URL must be restored to default: %s", mustRead(t, target))
	}
}

func TestMergeFilesOverrideAbsentFileEqualsEmptyPatch(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
	writeFile(t, override, `{"env":{"EXTRA":"added","LOCAL":"overwritten"}}`)

	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}

	// overrideファイル削除は空patchと等価で、追加keyは不存在・上書きkeyは元値へ復元される。
	if _, err := mergeFiles(target, fragment, ""); err != nil {
		t.Fatal(err)
	}
	text := string(mustRead(t, target))
	if strings.Contains(text, "EXTRA") {
		t.Fatalf("EXTRA must be absent after override deletion: %s", text)
	}
	if !strings.Contains(text, `"LOCAL": "keep"`) {
		t.Fatalf("LOCAL must be restored to original: %s", text)
	}
}

func TestMergeFilesOverrideMultipleRunsIdempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
	writeFile(t, override, `{"env":{"ANTHROPIC_BASE_URL":null,"EXTRA":"set"}}`)

	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	first := string(mustRead(t, target))

	for run := 2; run <= 3; run++ {
		changed, err := mergeFiles(target, fragment, override)
		if err != nil {
			t.Fatal(err)
		}
		if changed {
			t.Fatalf("run %d must be unchanged (idempotent)", run)
		}
		if string(mustRead(t, target)) != first {
			t.Fatalf("run %d target diverged", run)
		}
	}
}

func TestMergeFilesBrokenStatePreservesTargetAndStateBytes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
	writeFile(t, override, `{"env":{"EXTRA":"set"}}`)

	broken := []byte("{not valid json")
	statePath := statePathFor(target)
	if err := os.WriteFile(statePath, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	originalTarget := mustRead(t, target)

	_, err := mergeFiles(target, fragment, override)
	if err == nil || !strings.Contains(err.Error(), "state JSON") {
		t.Fatalf("error must reference state JSON: %v", err)
	}
	if !bytes.Equal(mustRead(t, target), originalTarget) {
		t.Fatalf("broken state must not modify target: %s", mustRead(t, target))
	}
	if !bytes.Equal(mustRead(t, statePath), broken) {
		t.Fatalf("broken state must not modify state sidecar: %s", mustRead(t, statePath))
	}
}

func TestMergeFilesUnsupportedStateVersionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
	writeFile(t, override, `{"env":{"EXTRA":"set"}}`)

	futureState := []byte(`{"version":99,"env":{}}`)
	statePath := statePathFor(target)
	if err := os.WriteFile(statePath, futureState, 0o600); err != nil {
		t.Fatal(err)
	}
	originalTarget := mustRead(t, target)

	_, err := mergeFiles(target, fragment, override)
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("error must reference unsupported version: %v", err)
	}
	if !bytes.Equal(mustRead(t, target), originalTarget) {
		t.Fatalf("unsupported version must not modify target: %s", mustRead(t, target))
	}
}

func TestMergeFilesOverrideStateIsPrivateAndAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, target, `{}`)
	writeFile(t, override, `{"env":{"EXTRA":"set"}}`)

	if _, err := mergeFiles(target, fragment, override); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(statePathFor(target))
	if err != nil {
		t.Fatal(err)
	}
	if stat.Mode().Perm() != 0o600 {
		t.Fatalf("state sidecar mode = %o, want 0600", stat.Mode().Perm())
	}
	var parsed overrideState
	if err := json.Unmarshal(mustRead(t, statePathFor(target)), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Version != overrideStateVersion {
		t.Fatalf("state version = %d, want %d", parsed.Version, overrideStateVersion)
	}
}

// main.goはstdlibのみをimportしGo module pathを含まない。新名称が現れるのは
// sidecar名等の端末local永続識別子の回帰のみであり、ここで検出する。
func TestMainSourceRetainsLegacySidecarIdentifier(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "codex-worker-orchestrator") {
		t.Fatalf("main.go must not reference renamed persistent identifier")
	}
}

func TestEnsureEnvMapHandlesAbsentAndNonMap(t *testing.T) {
	existing := map[string]any{"env": map[string]any{"K": "v"}}
	if got := ensureEnvMap(existing); got["K"] != "v" {
		t.Fatalf("ensureEnvMap must return existing env map: %#v", got)
	}
	absent := map[string]any{"other": 1}
	got := ensureEnvMap(absent)
	if len(got) != 0 {
		t.Fatalf("ensureEnvMap must return empty map for absent env: %#v", got)
	}
	nonMap := map[string]any{"env": "not-a-map"}
	if got := ensureEnvMap(nonMap); len(got) != 0 {
		t.Fatalf("ensureEnvMap must return empty map for non-map env: %#v", got)
	}
}

// writerFailingFirstはfailPathへの初回writeのみ失敗させ、以後(rollback含む)は成功させる。
// これによりforward書込み失敗→rollback復元→再実行収束を分離して検証する。
func writerFailingFirst(failPath string) writeFileFunc {
	calls := map[string]int{}
	return func(path string, data []byte, mode os.FileMode) error {
		calls[path]++
		if path == failPath && calls[path] == 1 {
			return fmt.Errorf("injected failure on %s", path)
		}
		return writeAtomic(path, data, mode)
	}
}

func assertPreserved(t *testing.T, path string, before fileRestore) {
	t.Helper()
	got := captureFileRestore(path)
	if got.existed != before.existed {
		t.Fatalf("%s existed=%v after failed run, want %v", path, got.existed, before.existed)
	}
	if !before.existed {
		return
	}
	if !bytes.Equal(got.data, before.data) {
		t.Fatalf("%s bytes not preserved after rollback\nwant %s\ngot  %s", path, before.data, got.data)
	}
	if got.mode != before.mode {
		t.Fatalf("%s mode=%o after rollback, want %o", path, got.mode, before.mode)
	}
}

func TestMergeFilesWriteFailureRollsBackAndConverges(t *testing.T) {
	cases := []struct {
		name             string
		failTarget       bool
		seedOverride     string
		failOverride     string
		convergeOverride string
		wantTargetHas    []string
		wantTargetNot    []string
		wantStateEnvKeys int
	}{
		{
			name:             "target write failure with existing state",
			failTarget:       true,
			seedOverride:     `{"env":{"EXTRA":"set"}}`,
			failOverride:     `{"env":{"EXTRA2":"set"}}`,
			convergeOverride: `{"env":{"EXTRA2":"set"}}`,
			wantTargetHas:    []string{`"EXTRA2": "set"`, `"LOCAL": "keep"`, `"MANAGED": "yes"`},
			wantTargetNot:    []string{`"EXTRA"`},
			wantStateEnvKeys: 1,
		},
		{
			// override解除でstate書込みが失敗しても空state+旧targetが残らないか。
			name:             "state write failure on override removal with existing state",
			failTarget:       false,
			seedOverride:     `{"env":{"EXTRA":"set"}}`,
			failOverride:     `{}`,
			convergeOverride: `{}`,
			wantTargetHas:    []string{`"LOCAL": "keep"`, `"MANAGED": "yes"`},
			wantTargetNot:    []string{`"EXTRA"`},
			wantStateEnvKeys: 0,
		},
		{
			name:             "target write failure without existing state",
			failTarget:       true,
			seedOverride:     "",
			failOverride:     `{"env":{"EXTRA":"set"}}`,
			convergeOverride: `{"env":{"EXTRA":"set"}}`,
			wantTargetHas:    []string{`"EXTRA": "set"`, `"LOCAL": "keep"`, `"MANAGED": "yes"`},
			wantStateEnvKeys: 1,
		},
		{
			name:             "state write failure without existing state",
			failTarget:       false,
			seedOverride:     "",
			failOverride:     `{"env":{"EXTRA":"set"}}`,
			convergeOverride: `{"env":{"EXTRA":"set"}}`,
			wantTargetHas:    []string{`"EXTRA": "set"`, `"LOCAL": "keep"`, `"MANAGED": "yes"`},
			wantStateEnvKeys: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "settings.json")
			fragment := filepath.Join(dir, "managed.json")
			override := filepath.Join(dir, "override.json")
			statePath := statePathFor(target)
			writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
			writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
			if err := os.Chmod(target, 0o640); err != nil {
				t.Fatal(err)
			}

			if tc.seedOverride != "" {
				writeFile(t, override, tc.seedOverride)
				if _, err := mergeFiles(target, fragment, override); err != nil {
					t.Fatal(err)
				}
			}

			writeFile(t, override, tc.failOverride)
			failPath := target
			if !tc.failTarget {
				failPath = statePath
			}
			beforeTarget := captureFileRestore(target)
			beforeState := captureFileRestore(statePath)

			_, err := mergeFilesWithWriter(target, fragment, override, writerFailingFirst(failPath))
			if err == nil || !strings.Contains(err.Error(), "injected failure") {
				t.Fatalf("got err=%v, want injected failure", err)
			}
			assertPreserved(t, target, beforeTarget)
			assertPreserved(t, statePath, beforeState)

			// rollback後の再実行で収束すること。1回目は変更あり、2回目は冪等。
			changed, err := mergeFiles(target, fragment, override)
			if err != nil {
				t.Fatal(err)
			}
			if !changed {
				t.Fatal("convergence re-run must report changed")
			}
			text := string(mustRead(t, target))
			for _, want := range tc.wantTargetHas {
				if !strings.Contains(text, want) {
					t.Fatalf("after converge missing %s in %s", want, text)
				}
			}
			for _, notWant := range tc.wantTargetNot {
				if strings.Contains(text, notWant) {
					t.Fatalf("after converge should not contain %s in %s", notWant, text)
				}
			}
			st, err := loadOverrideState(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if len(st.Env) != tc.wantStateEnvKeys {
				t.Fatalf("state env keys=%d after converge, want %d", len(st.Env), tc.wantStateEnvKeys)
			}

			changed2, err := mergeFiles(target, fragment, override)
			if err != nil {
				t.Fatal(err)
			}
			if changed2 {
				t.Fatal("second convergence run must be idempotent")
			}
			if string(mustRead(t, target)) != text {
				t.Fatal("target diverged after idempotent re-run")
			}
		})
	}
}

func TestMergeFilesRollbackFailureReportedWithOriginal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")
	fragment := filepath.Join(dir, "managed.json")
	override := filepath.Join(dir, "override.json")
	writeFile(t, fragment, `{"env":{"MANAGED":"yes"}}`)
	writeFile(t, target, `{"env":{"LOCAL":"keep"}}`)
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}
	writeFile(t, override, `{"env":{"EXTRA":"set"}}`)
	originalTarget := mustRead(t, target)

	alwaysFail := func(path string, data []byte, mode os.FileMode) error {
		return fmt.Errorf("injected failure on %s", path)
	}
	_, err := mergeFilesWithWriter(target, fragment, override, alwaysFail)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "injected failure") {
		t.Fatalf("error must contain original failure: %v", err)
	}
	if !strings.Contains(msg, "rollback失敗") {
		t.Fatalf("error must report rollback failure alongside original: %v", err)
	}
	if !bytes.Equal(mustRead(t, target), originalTarget) {
		t.Fatalf("target must stay at pre-transaction bytes when forward and rollback both fail: %s", mustRead(t, target))
	}
}
