#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/codex-worker-orchestrator-install-test.XXXXXX")

cleanup() {
    chmod -R u+w "$test_root" 2>/dev/null || true
    rm -rf "$test_root"
}
trap cleanup EXIT HUP INT TERM

# install.shはmktemp prefix等の非永続表示に新名称を使うが、端末local永続識別子
# (override path/env/sidecar/manifest)は旧codex-config名前空間を維持する。
for forbidden in \
    'CODEX_WORKER_ORCHESTRATOR_CLAUDE_SETTINGS_OVERRIDE' \
    'codex-worker-orchestrator/claude-settings.local.json' \
    '.codex-worker-orchestrator-claude-env-state.json' \
    '.codex-worker-orchestrator-managed-files'; do
    if grep -Fq "$forbidden" "$repo_root/install.sh"; then
        printf '%s\n' "install.sh must not reference renamed persistent identifier: $forbidden" >&2
        exit 1
    fi
done

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
grep -Fq 'DTSTART:YYYYMMDDTHHMMSS' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'DTSTART;TZID=Asia/Tokyo`は使わない' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'automations.next_run_at' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'verify-auto-resume' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq '返り値全体を文字列として必ず検査' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq '`suggested_create`を呼ばない' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'TELEMETRY_DIR' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '同じ責務・変更理由・検証単位' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'REPORT_ARTIFACT_DIR' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '同一tool orchestration内で完了までblocking wait' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '1回のwaitに最大待機時間を使い' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '短時間・固定間隔の反復wait' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '進捗報告目的のwake' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'ちょうど1物理行' "$success_case/codex/glm-worker/prompts/WORKER.md"
grep -Fq 'ARTIFACTS:' "$success_case/codex/glm-worker/prompts/REVIEWER.md"
test -f "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq '時間軸上の意味ある状態遷移' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq '意味変更の意図の有無に関わらず' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq 'override解除や削除後に残留' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq '親環境・別経路から再流入' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq 'silent no-op' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq '部分消失stateを安易に安全復旧' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq 'renameは既存環境を見落としやすく' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq 'state-transitions.md' "$success_case/codex/glm-worker/prompts/WORKER.md"
grep -Fq 'state-transitions.md' "$success_case/codex/glm-worker/prompts/REVIEWER.md"
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
grep -Fq 'packetまたは`STATUS: WORKER_ERROR`を含む結果' "$success_case/codex/AGENTS.md"
grep -Fq 'packetまたは`STATUS: WORKER_ERROR`を含む結果' "$success_case/codex/instructions/glm-packets.md"
(
    cd "$success_source"
    GLM_WORKER_HOME="$success_case/glm-home" \
        "$success_case/bin/glm-worker" --status \
        | grep -Fq 'ARTIFACT_DIR: none'
)

"$success_case/bin/glm-worker" --verify-auto-resume 2>&1 \
    | grep -Fq 'usage: glm-worker --verify-auto-resume'

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

override_source="$test_root/override-source"
override_case="$test_root/override-case"
copy_source "$override_source"

mkdir -p "$override_case/codex/rules" "$override_case/claude" "$override_case/xdg/codex-config"
printf '%s\n' '{"env":{"ANTHROPIC_BASE_URL":null,"ANTHROPIC_CUSTOM":"set-by-override"}}' \
    >"$override_case/xdg/codex-config/claude-settings.local.json"

HOME="$override_case/home" \
CODEX_CONFIG_DIR="$override_case/codex" \
GLM_WORKER_BIN_DIR="$override_case/bin" \
GLM_WORKER_HOME="$override_case/glm-home" \
CLAUDE_SETTINGS_FILE="$override_case/claude/settings.json" \
XDG_CONFIG_HOME="$override_case/xdg" \
    "$override_source/install.sh" >/dev/null

grep -Fq '"ANTHROPIC_CUSTOM": "set-by-override"' "$override_case/claude/settings.json"
if grep -Fq '"ANTHROPIC_BASE_URL"' "$override_case/claude/settings.json"; then
    printf '%s\n' 'override null should delete ANTHROPIC_BASE_URL' >&2
    exit 1
fi
grep -Fq '"ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.2"' "$override_case/claude/settings.json"

cp "$override_case/claude/settings.json" "$override_case/first.json"
HOME="$override_case/home" \
CODEX_CONFIG_DIR="$override_case/codex" \
GLM_WORKER_BIN_DIR="$override_case/bin" \
GLM_WORKER_HOME="$override_case/glm-home" \
CLAUDE_SETTINGS_FILE="$override_case/claude/settings.json" \
XDG_CONFIG_HOME="$override_case/xdg" \
    "$override_source/install.sh" >/dev/null
if ! cmp -s "$override_case/first.json" "$override_case/claude/settings.json"; then
    printf '%s\n' 'override install is not idempotent' >&2
    exit 1
fi

override_delete_source="$test_root/override-delete-source"
override_delete_case="$test_root/override-delete-case"
copy_source "$override_delete_source"
mkdir -p "$override_delete_case/claude"

HOME="$override_delete_case/home" \
CODEX_CONFIG_DIR="$override_delete_case/codex" \
GLM_WORKER_BIN_DIR="$override_delete_case/bin" \
GLM_WORKER_HOME="$override_delete_case/glm-home" \
CLAUDE_SETTINGS_FILE="$override_delete_case/claude/settings.json" \
XDG_CONFIG_HOME="$override_delete_case/xdg" \
    "$override_delete_source/install.sh" >/dev/null

grep -Fq '"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"' "$override_delete_case/claude/settings.json"

# override削除後に再installでmanaged Z.ai defaultsが復元され、override追加keyは不存在へ戻る
rm "$override_case/xdg/codex-config/claude-settings.local.json"
HOME="$override_case/home" \
CODEX_CONFIG_DIR="$override_case/codex" \
GLM_WORKER_BIN_DIR="$override_case/bin" \
GLM_WORKER_HOME="$override_case/glm-home" \
CLAUDE_SETTINGS_FILE="$override_case/claude/settings.json" \
XDG_CONFIG_HOME="$override_case/xdg" \
    "$override_source/install.sh" >/dev/null
grep -Fq '"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"' "$override_case/claude/settings.json"
if grep -Fq '"ANTHROPIC_CUSTOM"' "$override_case/claude/settings.json"; then
    printf '%s\n' 'override削除後にoverride追加key ANTHROPIC_CUSTOMが残っています' >&2
    exit 1
fi

bad_override_source="$test_root/bad-override-source"
bad_override_case="$test_root/bad-override-case"
copy_source "$bad_override_source"
mkdir -p "$bad_override_case/claude" "$bad_override_case/xdg/codex-config"
printf '%s\n' '{"env":{"BAD":[1,2]}}' >"$bad_override_case/xdg/codex-config/claude-settings.local.json"
printf '%s\n' '{"existing":"keep"}' >"$bad_override_case/claude/settings.json"
cp "$bad_override_case/claude/settings.json" "$bad_override_case/original.json"

if HOME="$bad_override_case/home" \
    CODEX_CONFIG_DIR="$bad_override_case/codex" \
    GLM_WORKER_BIN_DIR="$bad_override_case/bin" \
    GLM_WORKER_HOME="$bad_override_case/glm-home" \
    CLAUDE_SETTINGS_FILE="$bad_override_case/claude/settings.json" \
    XDG_CONFIG_HOME="$bad_override_case/xdg" \
    "$bad_override_source/install.sh" >/dev/null 2>&1; then
    printf '%s\n' 'malformed override should fail install' >&2
    exit 1
fi

if ! cmp -s "$bad_override_case/original.json" "$bad_override_case/claude/settings.json"; then
    printf '%s\n' 'malformed override must not modify settings.json (fail closed)' >&2
    exit 1
fi

# top-level null / env null overrideはinstallをfail closedさせsettings.jsonを書き換えない
for null_case in 'null' '{"env":null}'; do
    null_source="$test_root/null-override-source"
    null_case_dir="$test_root/null-override-case"
    copy_source "$null_source"
    mkdir -p "$null_case_dir/claude" "$null_case_dir/xdg/codex-config"
    printf '%s\n' "$null_case" >"$null_case_dir/xdg/codex-config/claude-settings.local.json"
    printf '%s\n' '{"existing":"keep"}' >"$null_case_dir/claude/settings.json"
    cp "$null_case_dir/claude/settings.json" "$null_case_dir/original.json"

    if HOME="$null_case_dir/home" \
        CODEX_CONFIG_DIR="$null_case_dir/codex" \
        GLM_WORKER_BIN_DIR="$null_case_dir/bin" \
        GLM_WORKER_HOME="$null_case_dir/glm-home" \
        CLAUDE_SETTINGS_FILE="$null_case_dir/claude/settings.json" \
        XDG_CONFIG_HOME="$null_case_dir/xdg" \
        "$null_source/install.sh" >/dev/null 2>&1; then
        printf '%s\n' "null override ($null_case) should fail install" >&2
        exit 1
    fi

    if ! cmp -s "$null_case_dir/original.json" "$null_case_dir/claude/settings.json"; then
        printf '%s\n' "null override ($null_case) must not modify settings.json" >&2
        exit 1
    fi
done

# 既存local keyの上書き/null削除をoverride解除で元値へ復元するinstaller統合確認
restore_source="$test_root/restore-source"
restore_case="$test_root/restore-case"
copy_source "$restore_source"
mkdir -p "$restore_case/claude" "$restore_case/xdg/codex-config"
printf '%s\n' '{"env":{"LOCAL_OVERWRITE":"orig","LOCAL_DELETE":"orig"}}' >"$restore_case/claude/settings.json"
printf '%s\n' '{"env":{"LOCAL_OVERWRITE":"new","LOCAL_DELETE":null}}' \
    >"$restore_case/xdg/codex-config/claude-settings.local.json"

HOME="$restore_case/home" \
CODEX_CONFIG_DIR="$restore_case/codex" \
GLM_WORKER_BIN_DIR="$restore_case/bin" \
GLM_WORKER_HOME="$restore_case/glm-home" \
CLAUDE_SETTINGS_FILE="$restore_case/claude/settings.json" \
XDG_CONFIG_HOME="$restore_case/xdg" \
    "$restore_source/install.sh" >/dev/null
grep -Fq '"LOCAL_OVERWRITE": "new"' "$restore_case/claude/settings.json"
if grep -Fq '"LOCAL_DELETE"' "$restore_case/claude/settings.json"; then
    printf '%s\n' 'null override must delete LOCAL_DELETE' >&2
    exit 1
fi

rm "$restore_case/xdg/codex-config/claude-settings.local.json"
HOME="$restore_case/home" \
CODEX_CONFIG_DIR="$restore_case/codex" \
GLM_WORKER_BIN_DIR="$restore_case/bin" \
GLM_WORKER_HOME="$restore_case/glm-home" \
CLAUDE_SETTINGS_FILE="$restore_case/claude/settings.json" \
XDG_CONFIG_HOME="$restore_case/xdg" \
    "$restore_source/install.sh" >/dev/null
grep -Fq '"LOCAL_OVERWRITE": "orig"' "$restore_case/claude/settings.json"
grep -Fq '"LOCAL_DELETE": "orig"' "$restore_case/claude/settings.json"

# 壊れたstate sidecarはinstallをfail closedさせtarget/stateとも書き換えない
broken_state_source="$test_root/broken-state-source"
broken_state_case="$test_root/broken-state-case"
copy_source "$broken_state_source"
mkdir -p "$broken_state_case/claude" "$broken_state_case/xdg/codex-config"
printf '%s\n' '{"env":{"EXISTING":"keep"}}' >"$broken_state_case/claude/settings.json"
printf '%s\n' '{"env":{"EXTRA":"set"}}' \
    >"$broken_state_case/xdg/codex-config/claude-settings.local.json"

HOME="$broken_state_case/home" \
CODEX_CONFIG_DIR="$broken_state_case/codex" \
GLM_WORKER_BIN_DIR="$broken_state_case/bin" \
GLM_WORKER_HOME="$broken_state_case/glm-home" \
CLAUDE_SETTINGS_FILE="$broken_state_case/claude/settings.json" \
XDG_CONFIG_HOME="$broken_state_case/xdg" \
    "$broken_state_source/install.sh" >/dev/null

state_file="$broken_state_case/claude/.codex-config-claude-env-state.json"
test -f "$state_file"
printf '%s\n' '{broken state' >"$state_file"
cp "$broken_state_case/claude/settings.json" "$broken_state_case/settings-before.json"
cp "$state_file" "$broken_state_case/state-before.json"

if HOME="$broken_state_case/home" \
    CODEX_CONFIG_DIR="$broken_state_case/codex" \
    GLM_WORKER_BIN_DIR="$broken_state_case/bin" \
    GLM_WORKER_HOME="$broken_state_case/glm-home" \
    CLAUDE_SETTINGS_FILE="$broken_state_case/claude/settings.json" \
    XDG_CONFIG_HOME="$broken_state_case/xdg" \
    "$broken_state_source/install.sh" >/dev/null 2>&1; then
    printf '%s\n' 'broken state should fail install' >&2
    exit 1
fi
if ! cmp -s "$broken_state_case/settings-before.json" "$broken_state_case/claude/settings.json"; then
    printf '%s\n' 'broken state must not modify settings.json' >&2
    exit 1
fi
if ! cmp -s "$broken_state_case/state-before.json" "$state_file"; then
    printf '%s\n' 'broken state must not modify state sidecar' >&2
    exit 1
fi

printf '%s\n' 'install smoke: PASS'
