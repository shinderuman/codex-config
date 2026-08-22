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
    installer_path=${3:-}

    HOME="$case_dir/home" \
    CODEX_CONFIG_DIR="$case_dir/codex" \
    GLM_WORKER_BIN_DIR="$case_dir/bin" \
    GLM_WORKER_HOME="$case_dir/glm-home" \
    CLAUDE_SETTINGS_FILE="$case_dir/claude/settings.json" \
    PATH="${installer_path:+$installer_path:}$PATH" \
        "$source_dir/install.sh"
}

prepare_preflight_failure_case() {
    source_dir=$1
    case_dir=$2

    copy_source "$source_dir"
    # self-protection testはtracked file前提で走るため、空repoではなく
    # 全fileをindexへ乗せた実運用相当のgit状態を作る。
    git -C "$source_dir" init -q
    git -C "$source_dir" add -A
    chmod 0644 "$source_dir/.githooks/post-merge"

    mkdir -p "$case_dir/codex" "$case_dir/claude"
    printf '%s\n' 'preflight-sentinel' >"$case_dir/codex/AGENTS.md"
    printf '%s\n' '{"existing":"keep"}' >"$case_dir/claude/settings.json"
    cp "$case_dir/claude/settings.json" "$case_dir/claude/original.json"
}

# 本物のgoへ結果ごと透過しつつ全呼出を記録し、forced_build_packageに一致する
# 末尾package引数のgo buildだけを失敗させるshimを作る。
# 失敗段階より後のpreflight commandやbuild_glm_worker/go runの実行有無は
# この呼出記録で段階ごとに固定する。
make_go_shim() {
    shim_dir=$1
    forced_build_package=$2

    mkdir -p "$shim_dir"
    real_go=$(command -v go)

    cat >"$shim_dir/go" <<EOF
#!/bin/sh
real_go='$real_go'
log_file='$shim_dir/invocations.log'
forced_build_package='$forced_build_package'

subcommand=\$1
for package in "\$@"; do
    :
done
module=\${PWD##*/}

if [ "\$subcommand" = build ] && [ -n "\$forced_build_package" ] && [ "\$package" = "\$forced_build_package" ]; then
    printf '%s %s forced-fail\n' "\$subcommand" "\$module" >>"\$log_file"
    printf 'go shim: forced build failure (%s)\n' "\$package" >&2
    exit 1
fi

"\$real_go" "\$@"
status=\$?
printf '%s %s %s\n' "\$subcommand" "\$module" "\$status" >>"\$log_file"
exit "\$status"
EOF
    chmod +x "$shim_dir/go"
}

expect_preflight_failure() {
    label=$1
    source_dir=$2
    case_dir=$3
    shim_dir=$4
    expected_log=$5

    if run_installer "$source_dir" "$case_dir" "$shim_dir"; then
        printf '%s\n' "preflight失敗($label)時にinstall.shが成功しました" >&2
        exit 1
    fi

    assert_preflight_refused "$source_dir" "$case_dir"

    if ! cmp -s "$shim_dir/invocations.log" "$expected_log"; then
        printf '%s\n' "preflight失敗($label)時のgo呼出順序が期待と異なります:" >&2
        cat "$shim_dir/invocations.log" >&2
        exit 1
    fi
}

assert_preflight_refused() {
    source_dir=$1
    case_dir=$2

    test "$(sed -n '1p' "$case_dir/codex/AGENTS.md")" = 'preflight-sentinel'
    test ! -e "$case_dir/bin"
    test ! -e "$case_dir/codex/config.toml"
    test ! -e "$case_dir/codex/rules/glm-worker.rules"
    test ! -e "$case_dir/codex/.codex-config-managed-files"
    test ! -d "$case_dir/glm-home"
    cmp -s "$case_dir/claude/settings.json" "$case_dir/claude/original.json"
    test ! -x "$source_dir/.githooks/post-merge"
    if git -C "$source_dir" config --local --get core.hooksPath >/dev/null 2>&1; then
        printf '%s\n' 'preflight失敗時にgit hookを有効化しました' >&2
        exit 1
    fi
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
test -f "$success_case/codex/instructions/feasibility-gate.md"
grep -Fq 'feasibility-gate.md' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'feasibility-gate.md' "$success_case/codex/AGENTS.md"
grep -Fq 'transport成功だけを成立性の証明にしない' "$success_case/codex/instructions/feasibility-gate.md"
test -f "$success_case/codex/instructions/task-lifecycle.md"
grep -Fq 'task-lifecycle.md' "$success_case/codex/AGENTS.md"
grep -Fq 'task-lifecycle.md' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '元依頼に診断・修正・再開確認が残る場合は親USER_REQUESTを完了扱いしない' "$success_case/codex/instructions/task-lifecycle.md"
grep -Fq '明示的に継続対象とした計画範囲が残る場合は親USER_REQUESTを完了扱いしない' "$success_case/codex/instructions/task-lifecycle.md"
test -f "$success_case/codex/instructions/failure-evidence.md"
grep -Fq 'failure-evidence.md' "$success_case/codex/AGENTS.md"
grep -Fq 'failure-evidence.md' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'failure-evidence.md' "$success_case/codex/instructions/glm-packets.md"
grep -Fq '全response・全成功応答の無条件保存はしない' "$success_case/codex/instructions/failure-evidence.md"
grep -Fq '秘密情報を生のまま保存させない' "$success_case/codex/instructions/failure-evidence.md"
grep -Fq '「判定不能」としてSol/ユーザーへ戻し、推測で修正を重ねさせない' "$success_case/codex/instructions/failure-evidence.md"
test -f "$success_case/codex/instructions/escaped-cause-layer.md"
grep -Fq 'escaped-cause-layer.md' "$success_case/codex/AGENTS.md"
grep -Fq 'escaped-cause-layer.md' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '通常の実装・調査task、review通過時の通常確認、新規依頼の受け付けへこの分類を要求しない' "$success_case/codex/instructions/escaped-cause-layer.md"
grep -Fq 'worker/reviewer promptへの個別checklist追加や個別gate追加だけで解決扱いにしない' "$success_case/codex/instructions/escaped-cause-layer.md"
grep -Fq '原因が本当にその層で発生したかを分類結果と照合する' "$success_case/codex/instructions/escaped-cause-layer.md"
test -f "$success_case/codex/instructions/git.md"
grep -Fq 'tracked canonical planのcommit同期' "$success_case/codex/instructions/git.md"
grep -Fq '初回commitとamendの間に停止・ユーザー報告でのturn終了・別task開始・GLM起動・install・handoffを行わず' "$success_case/codex/instructions/git.md"
grep -Fq 'amend失敗時はobsolete HEADのままinstall・次task・handoffへ進まず' "$success_case/codex/instructions/git.md"
grep -Fq 'commit・Git履歴操作' "$success_case/codex/AGENTS.md"
grep -Fq 'DTSTART:YYYYMMDDTHHMMSS' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'DTSTART;TZID=Asia/Tokyo`は使わない' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'automations.next_run_at' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'verify-auto-resume' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq '返り値全体を文字列として必ず検査' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq '`suggested_create`を呼ばない' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'Immediate automation creates cannot include DTSTART' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq '第一段階として、DTSTARTなし・status PAUSEDの最小placeholder schedule' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'RRULE:FREQ=HOURLY' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq '成功前にIDを推測・仮定しない' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq '作成済みplaceholder automationをbest-effortで削除' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'placeholderを作り直さない' "$success_case/codex/instructions/glm-auto-resume.md"
grep -Fq 'TELEMETRY_DIR' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '同じ責務・変更理由・検証単位' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'REPORT_ARTIFACT_DIR' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'direct-edit.md' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'REPOSITORY_LOCK' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'TASK_LIVENESS' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'GLM全体で同時実行不可' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'stale PIDやPID reuseでrunning扱いしない' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '確認済みの状態に変化がなくても発言しない' "$success_case/codex/instructions/glm-execution.md"
test -f "$success_case/codex/instructions/direct-edit.md"
grep -Fq '許可は、ユーザーが明示したaction class・成果物・変更理由へ限定' "$success_case/codex/instructions/direct-edit.md"
grep -Fq '同一session・同一目的・同一release・作業の連続性は、許可を別の行為へ拡張する理由にならない' "$success_case/codex/instructions/direct-edit.md"
grep -Fq 'ユーザーが「この具体的な設計変更・実装修正もCodex自身が直接行う」と改めて明示した場合だけ' "$success_case/codex/instructions/direct-edit.md"
grep -Fq '直接実行の許可はユーザーが明示した行為・成果物・変更理由に限定する' "$success_case/codex/AGENTS.md"
grep -Fq '同一session・同一目的・同一releaseを理由に拡張しない' "$success_case/codex/AGENTS.md"
grep -Fq 'direct-edit.md' "$success_case/codex/AGENTS.md"
grep -Fq '同一tool orchestration内で完了までblocking wait' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '1回のwaitに最大待機時間を使い' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '短時間・固定間隔の反復wait' "$success_case/codex/instructions/glm-execution.md"
grep -Fq '進捗報告目的のwake' "$success_case/codex/instructions/glm-execution.md"
grep -Fq 'NEEDS_SOL_DECISION' "$success_case/codex/glm-worker/prompts/WORKER.md"
grep -Fq '意味契約' "$success_case/codex/glm-worker/prompts/WORKER.md"
grep -Fq '構造化出力' "$success_case/codex/glm-worker/prompts/WORKER.md"
grep -Fq '構造化出力' "$success_case/codex/glm-worker/prompts/REVIEWER.md"
grep -Fq '`ARTIFACTS`: worker報告にある大容量成果物のうち最終結果に必要な絶対パスの配列' "$success_case/codex/glm-worker/prompts/REVIEWER.md"
test -f "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq '時間軸上の意味ある状態遷移' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq '意味変更の意図の有無に関わらず' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq 'override解除や削除後に残留' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq '親環境・別経路から再流入' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq 'silent no-op' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq '部分消失stateを安易に安全復旧' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq 'renameは既存環境を見落としやすく' "$success_case/codex/instructions/worker/state-transitions.md"
grep -Fq 'state-transitions.md' "$success_case/codex/glm-worker/prompts/WORKER.md"
grep -Fq 'verify_claude_cli || exit $?' "$repo_root/install.sh"
grep -Fq -- '--json-schema' "$repo_root/install.sh"
grep -Fq 'state-transitions.md' "$success_case/codex/glm-worker/prompts/REVIEWER.md"
test -f "$success_case/codex/instructions/local-unmanaged.md"
test ! -e "$success_case/codex/instructions/obsolete-managed.md"
test -f "$success_case/codex/rules/default.rules"
test -f "$success_case/claude/settings.json"
test -d "$success_case/glm-home/sessions"
test ! -d "$success_case/home/.glm-worker/sessions"
grep -Fq '"ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-4.7"' "$success_case/claude/settings.json"
grep -Fq '"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3"' "$success_case/claude/settings.json"
grep -Fq '"ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.3"' "$success_case/claude/settings.json"
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

(
    cd "$success_source"
    GLM_WORKER_HOME="$success_case/glm-home" \
        "$success_case/bin/glm-worker" --status \
        | grep -Fq 'REPOSITORY_LOCK: free'
)

"$success_case/bin/glm-worker" --verify-auto-resume 2>&1 \
    | grep -Fq 'usage: glm-worker --verify-auto-resume'

# 旧5.2/5.1 managed installからのupgradeでmanaged model keyだけ5.3へ置き換わる
upgrade_source="$test_root/upgrade-source"
upgrade_case="$test_root/upgrade-case"
copy_source "$upgrade_source"
mkdir -p "$upgrade_case/codex/rules" "$upgrade_case/claude"
printf '%s\n' 'model = "local-model"' >"$upgrade_case/codex/config.toml"
printf '%s\n' 'local rule' >"$upgrade_case/codex/rules/default.rules"
printf '%s\n' '{"local_setting":"keep","env":{"LOCAL_ENV":"keep","ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic","ANTHROPIC_DEFAULT_HAIKU_MODEL":"glm-4.7","ANTHROPIC_DEFAULT_SONNET_MODEL":"glm-5.1","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.2"}}' >"$upgrade_case/claude/settings.json"

run_installer "$upgrade_source" "$upgrade_case"

grep -Fq '"ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.3"' "$upgrade_case/claude/settings.json"
grep -Fq '"ANTHROPIC_DEFAULT_SONNET_MODEL": "glm-5.3"' "$upgrade_case/claude/settings.json"
grep -Fq '"ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-4.7"' "$upgrade_case/claude/settings.json"
if grep -Fq 'glm-5.2' "$upgrade_case/claude/settings.json" || grep -Fq 'glm-5.1' "$upgrade_case/claude/settings.json"; then
    printf '%s\n' 'upgrade must replace old managed model values' >&2
    exit 1
fi
grep -Fq '"local_setting": "keep"' "$upgrade_case/claude/settings.json"
grep -Fq '"LOCAL_ENV": "keep"' "$upgrade_case/claude/settings.json"

cp "$upgrade_case/claude/settings.json" "$upgrade_case/first.json"
run_installer "$upgrade_source" "$upgrade_case"
if ! cmp -s "$upgrade_case/first.json" "$upgrade_case/claude/settings.json"; then
    printf '%s\n' 'upgrade install is not idempotent' >&2
    exit 1
fi

# preflight各段階(glm-worker test/build、merge-json test/build)の失敗は、
# binary・managed files・Claude settings・git hookなどへの配置前に停止しなければならない。
# 各caseの期待呼出順序は、失敗段階より後のgo commandが一度も実行されていないことを固定する。
printf '%s\n' \
    'test glm-worker 1' \
    >"$test_root/expected-glm-worker-test-fail.log"
printf '%s\n' \
    'test glm-worker 0' \
    'build glm-worker forced-fail' \
    >"$test_root/expected-glm-worker-build-fail.log"
printf '%s\n' \
    'test glm-worker 0' \
    'build glm-worker 0' \
    'test merge-json 1' \
    >"$test_root/expected-merge-json-test-fail.log"
printf '%s\n' \
    'test glm-worker 0' \
    'build glm-worker 0' \
    'test merge-json 0' \
    'build merge-json forced-fail' \
    >"$test_root/expected-merge-json-build-fail.log"

glm_worker_test_fail_source="$test_root/glm-worker-test-fail-source"
glm_worker_test_fail_case="$test_root/glm-worker-test-fail-case"
glm_worker_test_fail_shim="$test_root/glm-worker-test-fail-shim"
prepare_preflight_failure_case "$glm_worker_test_fail_source" "$glm_worker_test_fail_case"
make_go_shim "$glm_worker_test_fail_shim" ''
cat >"$glm_worker_test_fail_source/glm-worker/internal/config/preflight_fail_test.go" <<'EOF'
package config

import "testing"

func TestInstallSmokePreflightFail(t *testing.T) {
    t.Fatal("install smoke: preflight failure probe")
}
EOF
expect_preflight_failure 'glm-worker test' \
    "$glm_worker_test_fail_source" "$glm_worker_test_fail_case" \
    "$glm_worker_test_fail_shim" "$test_root/expected-glm-worker-test-fail.log"

glm_worker_build_fail_source="$test_root/glm-worker-build-fail-source"
glm_worker_build_fail_case="$test_root/glm-worker-build-fail-case"
glm_worker_build_fail_shim="$test_root/glm-worker-build-fail-shim"
prepare_preflight_failure_case "$glm_worker_build_fail_source" "$glm_worker_build_fail_case"
make_go_shim "$glm_worker_build_fail_shim" './cmd/glm-worker'
expect_preflight_failure 'glm-worker build' \
    "$glm_worker_build_fail_source" "$glm_worker_build_fail_case" \
    "$glm_worker_build_fail_shim" "$test_root/expected-glm-worker-build-fail.log"

merge_json_test_fail_source="$test_root/merge-json-test-fail-source"
merge_json_test_fail_case="$test_root/merge-json-test-fail-case"
merge_json_test_fail_shim="$test_root/merge-json-test-fail-shim"
prepare_preflight_failure_case "$merge_json_test_fail_source" "$merge_json_test_fail_case"
make_go_shim "$merge_json_test_fail_shim" ''
cat >"$merge_json_test_fail_source/tools/merge-json/preflight_fail_test.go" <<'EOF'
package main

import "testing"

func TestInstallSmokePreflightFail(t *testing.T) {
    t.Fatal("install smoke: preflight failure probe")
}
EOF
expect_preflight_failure 'merge-json test' \
    "$merge_json_test_fail_source" "$merge_json_test_fail_case" \
    "$merge_json_test_fail_shim" "$test_root/expected-merge-json-test-fail.log"

merge_json_build_fail_source="$test_root/merge-json-build-fail-source"
merge_json_build_fail_case="$test_root/merge-json-build-fail-case"
merge_json_build_fail_shim="$test_root/merge-json-build-fail-shim"
prepare_preflight_failure_case "$merge_json_build_fail_source" "$merge_json_build_fail_case"
make_go_shim "$merge_json_build_fail_shim" '.'
expect_preflight_failure 'merge-json build' \
    "$merge_json_build_fail_source" "$merge_json_build_fail_case" \
    "$merge_json_build_fail_shim" "$test_root/expected-merge-json-build-fail.log"

# claude CLI shim。--help応答の文言で--json-schema契約支援有無を切り替え、呼出引数を
# 記録する。AI呼出は一切行わない。
make_claude_shim() {
    shim_dir=$1
    help_text=$2

    mkdir -p "$shim_dir"
    cat >"$shim_dir/claude" <<EOF
#!/bin/sh
log_file='$shim_dir/claude-invocations.log'

printf '%s\n' "\$*" >>"\$log_file"

if [ "\$1" = "--help" ]; then
    printf '%s\n' '$help_text'
    exit 0
fi

exit 1
EOF
    chmod +x "$shim_dir/claude"
}

# --json-schemaを案内しないclaude CLIではverify_claude_cliがpreflight通過後に
# installをfail closedさせ、binary・managed files・settingsへの配置は起きない。
# go呼出記録はpreflight全段階の成功だけを示し、claude呼出記録はprobe実行を固定する。
claude_probe_fail_source="$test_root/claude-probe-fail-source"
claude_probe_fail_case="$test_root/claude-probe-fail-case"
claude_probe_fail_shim="$test_root/claude-probe-fail-shim"
prepare_preflight_failure_case "$claude_probe_fail_source" "$claude_probe_fail_case"
make_go_shim "$claude_probe_fail_shim" ''
make_claude_shim "$claude_probe_fail_shim" 'usage: claude [-p prompt] [--model name]'
printf '%s\n' \
    'test glm-worker 0' \
    'build glm-worker 0' \
    'test merge-json 0' \
    'build merge-json 0' \
    >"$test_root/expected-claude-probe-fail.log"
expect_preflight_failure 'claude cli probe' \
    "$claude_probe_fail_source" "$claude_probe_fail_case" \
    "$claude_probe_fail_shim" "$test_root/expected-claude-probe-fail.log"
test "$(sed -n '1p' "$claude_probe_fail_shim/claude-invocations.log")" = '--help'
if [ -n "$(sed -n '2p' "$claude_probe_fail_shim/claude-invocations.log")" ]; then
    printf '%s\n' 'claude probeは--helpだけを呼び出さなければなりません' >&2
    exit 1
fi

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
grep -Fq '"ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.3"' "$override_case/claude/settings.json"

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

# tracked canonical planのfinal HEAD postcondition gate。
# 同期amendを飛ばした4cedc91型stale HEAD plan・削除済み/欠損ACTIVE task file参照・NEXTの
# 削除済み参照・NEXT/ACTIVEのtask path契約違反・閉じbacktick欠損等のmalformed bullet・
# ACTIVE重複・Git境界branch不一致は、go test/build・binary配置・managed files配置よりも
# 前にfail closedし、呼出・配置が一切起きないことを固定する。過渡表現の正当な
# amend後/uninstall前記述は通過させる。
make_plan_gate_repo() {
    source_dir=$1
    copy_source "$source_dir"
    git -C "$source_dir" init -q -b main
    git -C "$source_dir" config user.email t@example.com
    git -C "$source_dir" config user.name tester
    chmod 0644 "$source_dir/.githooks/post-merge"
    mkdir -p "$source_dir/IMPLEMENTATION_TASKS"
    printf '%s\n' '# Task: next' >"$source_dir/IMPLEMENTATION_TASKS/next-task.md"
    printf '%s\n' '# Task: future' >"$source_dir/IMPLEMENTATION_TASKS/future-task.md"
}

commit_plan_gate_repo() {
    git_dir=$1
    git -C "$git_dir" add -A
    git -C "$git_dir" commit -qm 'feat: task complete'
}

write_plan_gate_plan() {
    plan_dir=$1
    active_task=$2
    next_tasks=$3
    boundary_branch=$4
    stop_reason=$5
    next_operation=$6

    cat >"$plan_dir/IMPLEMENTATION_PLAN.local.md" <<EOF
# 実装index

## ACTIVE

- \`$active_task\`

## NEXT（優先順）
$next_tasks

## 現在のGit境界

- branch: \`$boundary_branch\`
- implementation baseline: 前task completion commit（current HEAD）
- metadata boundary: 前taskをHistoryへ移行してtask fileを削除し、次taskをACTIVEへ昇格
- push: 禁止

## 現在の停止理由

$stop_reason

## 次の親Codex操作

$next_operation
EOF
}

write_plan_gate_synced() {
    write_plan_gate_plan "$1" \
        'IMPLEMENTATION_TASKS/next-task.md' \
        '- `IMPLEMENTATION_TASKS/future-task.md`' \
        'main' \
        '前taskは完了。next-taskの開始前。' \
        'next-taskの要件を確認してGLM workerへ委譲する。'
}

expect_plan_gate_failure() {
    label=$1
    source_dir=$2
    case_dir=$3
    shim_dir=$4
    log_file=$5
    expected_reason=$6

    mkdir -p "$case_dir/codex" "$case_dir/claude"
    printf '%s\n' 'gate-sentinel' >"$case_dir/codex/AGENTS.md"

    if run_installer "$source_dir" "$case_dir" "$shim_dir" >"$log_file" 2>&1; then
        printf '%s\n' "plan gate失敗($label)時にinstall.shが成功しました" >&2
        exit 1
    fi

    test "$(sed -n '1p' "$case_dir/codex/AGENTS.md")" = 'gate-sentinel'
    test ! -f "$shim_dir/invocations.log"
    test ! -e "$case_dir/bin"
    test ! -e "$case_dir/codex/config.toml"
    test ! -e "$case_dir/codex/.codex-config-managed-files"
    test ! -e "$case_dir/claude/settings.json"
    test ! -d "$case_dir/glm-home"
    test ! -x "$source_dir/.githooks/post-merge"
    if git -C "$source_dir" config --local --get core.hooksPath >/dev/null 2>&1; then
        printf '%s\n' "plan gate失敗($label)時にgit hookを有効化しました" >&2
        exit 1
    fi
    grep -Fq "$expected_reason" "$log_file"
}

# 4cedc91型stale HEAD。完了済みcommitの同期amendを飛ばし、停止理由・次の親Codex操作が
# amend直前・install前の過渡表現を残したplanをfinal HEADへcommitしている。
stale_source="$test_root/plan-gate-stale-source"
stale_case="$test_root/plan-gate-stale-case"
stale_shim="$test_root/plan-gate-stale-shim"
make_plan_gate_repo "$stale_source"
write_plan_gate_plan "$stale_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskの実装・test・review・commitは完了したが、plan/historyの同期を同一commitへamendする直前。' \
    'amend後に`install.sh`で本配置と一致を確認し、next-taskを開始する。'
commit_plan_gate_repo "$stale_source"
make_go_shim "$stale_shim" ''
expect_plan_gate_failure '4cedc91型stale HEAD' \
    "$stale_source" "$stale_case" "$stale_shim" \
    "$test_root/plan-gate-stale.log" \
    '現在状態記述が完了済みcommitの操作を未実施としています'

# 完了task fileを削除したcommit後もplanがACTIVE参照を残している削除済みACTIVE。
deleted_active_source="$test_root/plan-gate-deleted-active-source"
deleted_active_case="$test_root/plan-gate-deleted-active-case"
deleted_active_shim="$test_root/plan-gate-deleted-active-shim"
make_plan_gate_repo "$deleted_active_source"
write_plan_gate_synced "$deleted_active_source"
commit_plan_gate_repo "$deleted_active_source"
git -C "$deleted_active_source" rm -q IMPLEMENTATION_TASKS/next-task.md
git -C "$deleted_active_source" commit -qm 'chore: remove completed task'
make_go_shim "$deleted_active_shim" ''
expect_plan_gate_failure '削除済みACTIVE参照' \
    "$deleted_active_source" "$deleted_active_case" "$deleted_active_shim" \
    "$test_root/plan-gate-deleted-active.log" \
    'IMPLEMENTATION_TASKS/next-task.md がHEAD treeへregular fileとして存在しません'

# ACTIVE欄がHEAD treeに存在しないtask fileを指す欠損ACTIVE file。
missing_active_source="$test_root/plan-gate-missing-active-source"
missing_active_case="$test_root/plan-gate-missing-active-case"
missing_active_shim="$test_root/plan-gate-missing-active-shim"
make_plan_gate_repo "$missing_active_source"
write_plan_gate_plan "$missing_active_source" \
    'IMPLEMENTATION_TASKS/ghost-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskは完了。ghost-taskの開始前。' \
    'ghost-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$missing_active_source"
make_go_shim "$missing_active_shim" ''
expect_plan_gate_failure '欠損ACTIVE file' \
    "$missing_active_source" "$missing_active_case" "$missing_active_shim" \
    "$test_root/plan-gate-missing-active.log" \
    'IMPLEMENTATION_TASKS/ghost-task.md がHEAD treeへregular fileとして存在しません'

# NEXTが削除済みtask fileを参照している場合も同じpostcondition違反として拒否する。
next_missing_source="$test_root/plan-gate-next-missing-source"
next_missing_case="$test_root/plan-gate-next-missing-case"
next_missing_shim="$test_root/plan-gate-next-missing-shim"
make_plan_gate_repo "$next_missing_source"
write_plan_gate_plan "$next_missing_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/vanished-task.md`' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$next_missing_source"
make_go_shim "$next_missing_shim" ''
expect_plan_gate_failure 'NEXTの削除済み参照' \
    "$next_missing_source" "$next_missing_case" "$next_missing_shim" \
    "$test_root/plan-gate-next-missing.log" \
    'IMPLEMENTATION_TASKS/vanished-task.md がHEAD treeへregular fileとして存在しません'

# NEXTのbulletがtask pathへ解決できない場合もfail closedする。認識できないbulletを
# 黙って無視するとinvalid scheduled entryを含むHEADがgateを通過する。
next_garbage_source="$test_root/plan-gate-next-garbage-source"
next_garbage_case="$test_root/plan-gate-next-garbage-case"
next_garbage_shim="$test_root/plan-gate-next-garbage-shim"
make_plan_gate_repo "$next_garbage_source"
write_plan_gate_plan "$next_garbage_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- garbage' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$next_garbage_source"
make_go_shim "$next_garbage_shim" ''
expect_plan_gate_failure 'NEXT非task項目' \
    "$next_garbage_source" "$next_garbage_case" "$next_garbage_shim" \
    "$test_root/plan-gate-next-garbage.log" \
    'NEXT/BLOCKED欄にtask pathへ解決できない項目があります: garbage'

# NEXTのbulletが配置契約外のpathを指す場合も同じfail closedで拒否する。
next_outside_source="$test_root/plan-gate-next-outside-source"
next_outside_case="$test_root/plan-gate-next-outside-case"
next_outside_shim="$test_root/plan-gate-next-outside-shim"
make_plan_gate_repo "$next_outside_source"
write_plan_gate_plan "$next_outside_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `tasks/future-task.md`' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$next_outside_source"
make_go_shim "$next_outside_shim" ''
expect_plan_gate_failure 'NEXT配置契約外path' \
    "$next_outside_source" "$next_outside_case" "$next_outside_shim" \
    "$test_root/plan-gate-next-outside.log" \
    'NEXT/BLOCKED欄にtask pathへ解決できない項目があります: tasks/future-task.md'

# ACTIVE欄自体が配置契約外のpathを指す場合も共通task path契約で拒否する。
active_outside_source="$test_root/plan-gate-active-outside-source"
active_outside_case="$test_root/plan-gate-active-outside-case"
active_outside_shim="$test_root/plan-gate-active-outside-shim"
make_plan_gate_repo "$active_outside_source"
write_plan_gate_plan "$active_outside_source" \
    'tasks/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$active_outside_source"
make_go_shim "$active_outside_shim" ''
expect_plan_gate_failure 'ACTIVE配置契約違反' \
    "$active_outside_source" "$active_outside_case" "$active_outside_shim" \
    "$test_root/plan-gate-active-outside.log" \
    'ACTIVE欄がtask path契約(IMPLEMENTATION_TASKS/配下の.md・配置契約準拠)へ違反しています: tasks/next-task.md'

# 閉じbacktickのないbulletはruntime抽出とinstaller抽出の受理集合を揃えるため、
# ACTIVE/NEXT/BLOCKEDすべてでmalformedとしてfail closedする。
active_unclosed_source="$test_root/plan-gate-active-unclosed-source"
active_unclosed_case="$test_root/plan-gate-active-unclosed-case"
active_unclosed_shim="$test_root/plan-gate-active-unclosed-shim"
make_plan_gate_repo "$active_unclosed_source"
write_plan_gate_plan "$active_unclosed_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
sed -e 's/^- `IMPLEMENTATION_TASKS\/next-task.md`$/- `IMPLEMENTATION_TASKS\/next-task.md/' \
    "$active_unclosed_source/IMPLEMENTATION_PLAN.local.md" >"$active_unclosed_source/plan.tmp"
mv "$active_unclosed_source/plan.tmp" "$active_unclosed_source/IMPLEMENTATION_PLAN.local.md"
commit_plan_gate_repo "$active_unclosed_source"
make_go_shim "$active_unclosed_shim" ''
expect_plan_gate_failure 'ACTIVE閉じbacktick欠損' \
    "$active_unclosed_source" "$active_unclosed_case" "$active_unclosed_shim" \
    "$test_root/plan-gate-active-unclosed.log" \
    'ACTIVE欄にbullet構文(逆引用符1組で囲まれた単一task path、または逆引用符なしの直書き)へ違反している項目があります'

# 閉じbacktickの後ろに余分なtextがあればmalformedとして拒否する。
active_suffix_source="$test_root/plan-gate-active-suffix-source"
active_suffix_case="$test_root/plan-gate-active-suffix-case"
active_suffix_shim="$test_root/plan-gate-active-suffix-shim"
make_plan_gate_repo "$active_suffix_source"
write_plan_gate_plan "$active_suffix_source" \
    'IMPLEMENTATION_TASKS/next-task.md` (次task)' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$active_suffix_source"
make_go_shim "$active_suffix_shim" ''
expect_plan_gate_failure 'ACTIVE余分なsuffix' \
    "$active_suffix_source" "$active_suffix_case" "$active_suffix_shim" \
    "$test_root/plan-gate-active-suffix.log" \
    'ACTIVE欄にbullet構文(逆引用符1組で囲まれた単一task path、または逆引用符なしの直書き)へ違反している項目があります'

# 複数backtick組もmalformedとして拒否する。
active_multi_source="$test_root/plan-gate-active-multi-source"
active_multi_case="$test_root/plan-gate-active-multi-case"
active_multi_shim="$test_root/plan-gate-active-multi-shim"
make_plan_gate_repo "$active_multi_source"
write_plan_gate_plan "$active_multi_source" \
    'IMPLEMENTATION_TASKS/next-task.md` `IMPLEMENTATION_TASKS/future-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$active_multi_source"
make_go_shim "$active_multi_shim" ''
expect_plan_gate_failure 'ACTIVE複数backtick組' \
    "$active_multi_source" "$active_multi_case" "$active_multi_shim" \
    "$test_root/plan-gate-active-multi.log" \
    'ACTIVE欄にbullet構文(逆引用符1組で囲まれた単一task path、または逆引用符なしの直書き)へ違反している項目があります'

# NEXTの閉じbacktick欠損も同じmalformed拒否へ流れる。
next_unclosed_source="$test_root/plan-gate-next-unclosed-source"
next_unclosed_case="$test_root/plan-gate-next-unclosed-case"
next_unclosed_shim="$test_root/plan-gate-next-unclosed-shim"
make_plan_gate_repo "$next_unclosed_source"
write_plan_gate_plan "$next_unclosed_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$next_unclosed_source"
make_go_shim "$next_unclosed_shim" ''
expect_plan_gate_failure 'NEXT閉じbacktick欠損' \
    "$next_unclosed_source" "$next_unclosed_case" "$next_unclosed_shim" \
    "$test_root/plan-gate-next-unclosed.log" \
    'NEXT/BLOCKED欄にbullet構文(逆引用符1組で囲まれた単一task path、または逆引用符なしの直書き)へ違反している項目があります'

# BLOCKED欄の閉じbacktick欠損も同じmalformed拒否へ流れる。
blocked_unclosed_source="$test_root/plan-gate-blocked-unclosed-source"
blocked_unclosed_case="$test_root/plan-gate-blocked-unclosed-case"
blocked_unclosed_shim="$test_root/plan-gate-blocked-unclosed-shim"
make_plan_gate_repo "$blocked_unclosed_source"
write_plan_gate_plan "$blocked_unclosed_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
printf '\n## BLOCKED / USER_PERMISSION_WAIT\n\n- `IMPLEMENTATION_TASKS/future-task.md\n' \
    >>"$blocked_unclosed_source/IMPLEMENTATION_PLAN.local.md"
commit_plan_gate_repo "$blocked_unclosed_source"
make_go_shim "$blocked_unclosed_shim" ''
expect_plan_gate_failure 'BLOCKED閉じbacktick欠損' \
    "$blocked_unclosed_source" "$blocked_unclosed_case" "$blocked_unclosed_shim" \
    "$test_root/plan-gate-blocked-unclosed.log" \
    'NEXT/BLOCKED欄にbullet構文(逆引用符1組で囲まれた単一task path、または逆引用符なしの直書き)へ違反している項目があります'

# NEXT昇格を忘れて完了済みACTIVE taskをNEXTへ残す重複記載。
active_dup_source="$test_root/plan-gate-active-dup-source"
active_dup_case="$test_root/plan-gate-active-dup-case"
active_dup_shim="$test_root/plan-gate-active-dup-shim"
make_plan_gate_repo "$active_dup_source"
write_plan_gate_plan "$active_dup_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/next-task.md`
- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$active_dup_source"
make_go_shim "$active_dup_shim" ''
expect_plan_gate_failure 'ACTIVE重複記載' \
    "$active_dup_source" "$active_dup_case" "$active_dup_shim" \
    "$test_root/plan-gate-active-dup.log" \
    'IMPLEMENTATION_TASKS/next-task.md がNEXT/BLOCKEDへ重複して記載されています'

# PlanのGit境界branchがHEADの実際のbranchと矛盾している場合を拒否する。
branch_mismatch_source="$test_root/plan-gate-branch-mismatch-source"
branch_mismatch_case="$test_root/plan-gate-branch-mismatch-case"
branch_mismatch_shim="$test_root/plan-gate-branch-mismatch-shim"
make_plan_gate_repo "$branch_mismatch_source"
write_plan_gate_plan "$branch_mismatch_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'feature-x' \
    '前taskは完了。next-taskの開始前。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$branch_mismatch_source"
make_go_shim "$branch_mismatch_shim" ''
expect_plan_gate_failure 'HEAD境界不一致' \
    "$branch_mismatch_source" "$branch_mismatch_case" "$branch_mismatch_shim" \
    "$test_root/plan-gate-branch-mismatch.log" \
    'Git境界branch feature-xが現在のbranch(main)と矛盾しています'

# amend失敗(pre-commit hook拒否)でHEADがstaleのまま残ってもgateは拒否し続け、
# 同期済みplanへの同一commit復旧amendだけで通過する。
amend_fail_source="$test_root/plan-gate-amend-fail-source"
amend_fail_case="$test_root/plan-gate-amend-fail-case"
amend_fail_shim="$test_root/plan-gate-amend-fail-shim"
make_plan_gate_repo "$amend_fail_source"
write_plan_gate_plan "$amend_fail_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskの実装・test・review・commitは完了したが、plan/historyの同期を同一commitへamendする直前。' \
    'amend後に`install.sh`で本配置と一致を確認し、next-taskを開始する。'
commit_plan_gate_repo "$amend_fail_source"
make_go_shim "$amend_fail_shim" ''
expect_plan_gate_failure 'amend失敗後もstale' \
    "$amend_fail_source" "$amend_fail_case" "$amend_fail_shim" \
    "$test_root/plan-gate-amend-fail.log" \
    '現在状態記述が完了済みcommitの操作を未実施としています'

mkdir -p "$amend_fail_source/.git/hooks"
printf '%s\n' '#!/bin/sh' 'exit 1' >"$amend_fail_source/.git/hooks/pre-commit"
chmod +x "$amend_fail_source/.git/hooks/pre-commit"
if git -C "$amend_fail_source" commit --amend --no-edit >/dev/null 2>&1; then
    printf '%s\n' 'pre-commit hook拒否によるamend失敗再現に失敗しました' >&2
    exit 1
fi
rm "$amend_fail_source/.git/hooks/pre-commit"

# 同期済みfinal HEADへの同一commit復旧amend。追加commitを挟まず同じHEADへ同期内容を
# 収めた後はgateを通過し、本配置まで進む。
write_plan_gate_synced "$amend_fail_source"
git -C "$amend_fail_source" add -A
git -C "$amend_fail_source" commit --amend --no-edit -q
run_installer "$amend_fail_source" "$test_root/plan-gate-amend-recover-case" \
    >"$test_root/plan-gate-amend-recover.log" 2>&1
grep -Fq 'plan final head: verified' "$test_root/plan-gate-amend-recover.log"
test -x "$test_root/plan-gate-amend-recover-case/bin/glm-worker"

# 同期済みfinal HEADは本配置まで進む。working treeのplanを未commitのstale内容へ
# 書き換えても、gateはHEADだけを判定するため影響しない。
synced_source="$test_root/plan-gate-synced-source"
synced_case="$test_root/plan-gate-synced-case"
make_plan_gate_repo "$synced_source"
write_plan_gate_synced "$synced_source"
commit_plan_gate_repo "$synced_source"
run_installer "$synced_source" "$synced_case" >"$test_root/plan-gate-synced.log" 2>&1
grep -Fq 'plan final head: verified' "$test_root/plan-gate-synced.log"
test -x "$synced_case/bin/glm-worker"

write_plan_gate_plan "$synced_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskの実装・test・review・commitは完了したが、plan/historyの同期を同一commitへamendする直前。' \
    'amend後に`install.sh`で本配置と一致を確認し、next-taskを開始する。'
run_installer "$synced_source" "$synced_case" >"$test_root/plan-gate-dirty.log" 2>&1
grep -Fq 'plan final head: verified' "$test_root/plan-gate-dirty.log"

# 過渡表現patternはpending操作だけを対象にする。「amend後のpostcondition」は現在taskの
# 正当な記述、「uninstall前」は英数字identifier境界で別語のため、いずれも拒否しない。
positive_source="$test_root/plan-gate-positive-source"
positive_case="$test_root/plan-gate-positive-case"
make_plan_gate_repo "$positive_source"
write_plan_gate_plan "$positive_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskは完了。next-taskはamend後のpostcondition検証とuninstall前のsettings確認を含む。' \
    'next-taskの要件を確認してGLM workerへ委譲する。'
commit_plan_gate_repo "$positive_source"
run_installer "$positive_source" "$positive_case" >"$test_root/plan-gate-positive.log" 2>&1
grep -Fq 'plan final head: verified' "$test_root/plan-gate-positive.log"
test -x "$positive_case/bin/glm-worker"

# planをGit indexへ置かないrepositoryではgateはskipしてHEAD postconditionの適用外と
# なり、過渡的なworking tree planを理由に配置を拒否しない。skip後にpreflight段階まで
# 到達したこととgate失敗案内が出ないことを固定する。なお本source copyのpreflightは
# tracked canonical planを要求する自己保護testを別契約として持つため、untracked planの
# installはpreflightで失敗する。gate側のskipだけを本scenarioの対象とする。
untracked_source="$test_root/plan-gate-untracked-source"
untracked_case="$test_root/plan-gate-untracked-case"
make_plan_gate_repo "$untracked_source"
rm "$untracked_source/IMPLEMENTATION_PLAN.local.md"
commit_plan_gate_repo "$untracked_source"
write_plan_gate_plan "$untracked_source" \
    'IMPLEMENTATION_TASKS/next-task.md' \
    '- `IMPLEMENTATION_TASKS/future-task.md`' \
    'main' \
    '前taskの実装・test・review・commitは完了したが、plan/historyの同期を同一commitへamendする直前。' \
    'amend後に`install.sh`で本配置と一致を確認し、next-taskを開始する。'
if run_installer "$untracked_source" "$untracked_case" >"$test_root/plan-gate-untracked.log" 2>&1; then
    printf '%s\n' 'untracked plan repositoryのinstall.shが成功しました' >&2
    exit 1
fi
grep -Fq 'plan final head: skipped (IMPLEMENTATION_PLAN.local.md is untracked)' \
    "$test_root/plan-gate-untracked.log"
grep -Fq 'preflight: validating source before applying managed files' \
    "$test_root/plan-gate-untracked.log"
if grep -Fq '同一commitへamendしてからinstallすること' "$test_root/plan-gate-untracked.log"; then
    printf '%s\n' 'untracked plan repositoryでplan gateが拒否しました' >&2
    exit 1
fi
test ! -e "$untracked_case/bin"

printf '%s\n' 'install smoke: PASS'
