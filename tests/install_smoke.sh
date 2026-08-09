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

run_installer "$success_source" "$success_case"
run_installer "$success_source" "$success_case"

test -x "$success_case/bin/glm-worker"
test -f "$success_case/codex/AGENTS.md"
test -f "$success_case/codex/rules/glm-worker.rules"
test -f "$success_case/claude/settings.json"
test -d "$success_case/glm-home/sessions"
test ! -d "$success_case/home/.glm-worker/sessions"

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
