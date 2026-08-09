#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/codex-config-install-test.XXXXXX")

cleanup() {
    chmod -R u+w "$test_root" 2>/dev/null || true
    rm -rf "$test_root"
}
trap cleanup EXIT HUP INT TERM

copy_source() {
    destination=$1
    mkdir -p "$destination"
    rsync -a --exclude '.git' "$repo_root/" "$destination/"
}

run_installer() {
    source_dir=$1
    case_dir=$2

    HOME="$case_dir/home" \
    CODEX_CONFIG_DIR="$case_dir/codex" \
    GLM_WORKER_BIN_DIR="$case_dir/bin" \
    GLM_WORKER_HOME="$case_dir/glm-home" \
    CLAUDE_SETTINGS_FILE="$case_dir/claude/settings.json" \
        "$source_dir/install.sh"
}

success_source="$test_root/success-source"
success_case="$test_root/success-case"
copy_source "$success_source"

mkdir -p "$success_case/codex/rules" "$success_case/claude"
printf '%s\n' 'model = "local-model"' >"$success_case/codex/config.toml"
printf '%s\n' 'local rule' >"$success_case/codex/rules/default.rules"
printf '%s\n' '{"local_setting":"keep","env":{"LOCAL_ENV":"keep"}}' >"$success_case/claude/settings.json"

run_installer "$success_source" "$success_case"

mkdir -p "$success_case/codex/instructions"
printf '%s\n' 'old managed file' >"$success_case/codex/instructions/obsolete-managed.md"
printf '%s\n' 'local unmanaged file' >"$success_case/codex/instructions/local-unmanaged.md"
printf '%s\n' 'instructions/obsolete-managed.md' >>"$success_case/codex/.codex-config-managed-files"

run_installer "$success_source" "$success_case"

test -x "$success_case/bin/glm-worker"
test -f "$success_case/codex/AGENTS.md"
test -f "$success_case/codex/instructions/glm-auto-resume.md"
test -f "$success_case/codex/rules/glm-worker.rules"
test -f "$success_case/codex/instructions/glm-execution.md"
test -f "$success_case/codex/instructions/glm-packets.md"
test -f "$success_case/codex/instructions/local-unmanaged.md"
test ! -e "$success_case/codex/instructions/obsolete-managed.md"
test -f "$success_case/codex/rules/default.rules"
test -f "$success_case/claude/settings.json"
test -d "$success_case/glm-home/sessions"
test ! -d "$success_case/home/.glm-worker/sessions"
grep -Fq '"ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-4.7"' "$success_case/claude/settings.json"
grep -Fq '"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.1"' "$success_case/claude/settings.json"
grep -Fq '"ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.2"' "$success_case/claude/settings.json"
grep -Fq '"local_setting": "keep"' "$success_case/claude/settings.json"
grep -Fq '"LOCAL_ENV": "keep"' "$success_case/claude/settings.json"
grep -Fq 'model = "local-model"' "$success_case/codex/config.toml"
grep -Fq 'background_terminal_max_timeout = 21600000' "$success_case/codex/config.toml"
grep -Fq '初回低リスクreviewはGLM-4.7 / high' "$success_case/codex/AGENTS.md"
(
    cd "$success_source"
    GLM_WORKER_HOME="$success_case/glm-home" \
        "$success_case/bin/glm-worker" --status \
        | grep -Fq 'TASK_STATUS: none'
)

failure_source="$test_root/failure-source"
failure_case="$test_root/failure-case"
copy_source "$failure_source"
mkdir -p "$failure_case/codex"
printf '%s\n' 'preflight-sentinel' >"$failure_case/codex/AGENTS.md"
printf '%s\n' 'this is not valid Go' >>"$failure_source/glm-worker/internal/config/config.go"

if run_installer "$failure_source" "$failure_case"; then
    printf '%s\n' 'preflight失敗時にinstall.shが成功しました' >&2
    exit 1
fi

test "$(sed -n '1p' "$failure_case/codex/AGENTS.md")" = 'preflight-sentinel'
test ! -e "$failure_case/bin/glm-worker"

printf '%s\n' 'install smoke: PASS'
