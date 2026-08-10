#!/usr/bin/env bash
# isolation-smoke.sh — Claude Code worker/reviewer 入力隔離の統合smoke (実Z.ai呼出1回)
#
# glm-worker の起動隔離構成(--safe-mode / 空 --setting-sources / --strict-mcp-config +
# 空 --mcp-config / --disable-slash-commands / inline隔離 --settings / Z.ai allowlist env)
# が、現行 claude CLI で user/project/local CLAUDE.md・rules・auto-memory のpoison marker
# を実際に遮断し、glm-worker明示prompt markerだけ応答へ残すことを1回の実claude起動で検証する。
#
# 【注意】このscriptは外部ネットワーク(Z.ai API)へ通信する。go test / 通常のoffline
# smokeへは統合せず、手動または專用stepでのみ実行する。再実行可能。
#
# 使い方:
#   CLAUDE_BIN=/path/to/claude glm-worker/scripts/isolation-smoke.sh
#   (既定CLAUDE_BIN=claude。Z.ai設定は ~/.claude/settings.json のenv blockから
#    allowlist分だけ抽出して子process envへ注入する。secretをstdoutへは出さない。)
#
# 終了code: 0=隔離OK(明示markerあり/poisonなし), 1=poison検出または明示marker不在,
#           2=前提エター(Z.ai設定不在・claude起動失敗等)
set -euo pipefail

CLAUDE_BIN="${CLAUDE_BIN:-claude}"
# 子processへ渡す前に現CLAUDE_CONFIG_DIR(既定~/.claude)を実settings sourceとして扱う。
REAL_CFG="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
REAL_SETTINGS="$REAL_CFG/settings.json"

if [[ ! -x "$(command -v jq 2>/dev/null || true)" ]]; then
	echo "ERROR: jqが必要です" >&2
	exit 2
fi
if [[ ! -f "$REAL_SETTINGS" ]]; then
	echo "ERROR: Z.ai settings不在: $REAL_SETTINGS" >&2
	exit 2
fi

extract_env() {
	# allowlist keyをsettings.jsonのenv blockから取り出す。不在なら空。
	jq -r --arg k "$1" '.env[$k] // empty' "$REAL_SETTINGS"
}

ZAI_TOKEN="$(extract_env ANTHROPIC_AUTH_TOKEN)"
ZAI_BASE="$(extract_env ANTHROPIC_BASE_URL)"
if [[ -z "$ZAI_TOKEN" || -z "$ZAI_BASE" ]]; then
	echo "ERROR: ANTHROPIC_AUTH_TOKEN/ANTHROPIC_BASE_URL が $REAL_SETTINGS にありません" >&2
	exit 2
fi

# --- 作業用の一時Claude configと一時repoへpoison markerを配置 ---
WORK="$(mktemp -d -t glm-isolation-smoke)"
trap 'rm -rf "$WORK"' EXIT
TMPCFG="$WORK/claude-config"
TMPREPO="$WORK/repo"
mkdir -p "$TMPCFG" "$TMPREPO"

# user global memoryとrules (claudeMdExcludesの絶対path/globで除外されるべき)
mkdir -p "$TMPCFG/rules"
printf '# user global CLAUDE.md\nPOISON_USER_GLOBAL marker here.\n' >"$TMPCFG/CLAUDE.md"
printf '# user rules\nPOISON_RULES marker here.\n' >"$TMPCFG/rules/extra.md"

# projectとlocal CLAUDE.md (safe-mode + **/CLAUDE{,.local}.md で除外されるべき)
printf '# project CLAUDE.md\nPOISON_PROJECT marker here.\n' >"$TMPREPO/CLAUDE.md"
printf '# local CLAUDE.local.md\nPOISON_LOCAL marker here.\n' >"$TMPREPO/CLAUDE.local.md"

# auto-memory: <cfg>/projects/<encoded-cwd>/memory/*.md (autoMemoryEnabled:falseで無効化されるべき)
ENCODED="$(printf '%s' "$TMPREPO" | tr '/' '-')"
MEMDIR="$TMPCFG/projects/$ENCODED/memory"
mkdir -p "$MEMDIR"
printf 'POISON_AUTOMEMORY marker here.\n' >"$MEMDIR/MEMORY.md"
printf 'POISON_AUTOMEMORY_EXTRA marker here.\n' >"$MEMDIR/feedback.md"

# --- glm-worker明示prompt (append-system-prompt-file)。GLM_MARKER_OKを仕込む ---
SYSFILE="$WORK/worker-prompt.md"
cat >"$SYSFILE" <<'EOF'
あなたはglm-worker配下のClaude Code workerです。
GLM_MARKER_OK は隔離された明示prompt経路のmarkerです。
EOF

# --- 隔離settings (runner.go:isolationSettings と同一構成) ---
ISO_SETTINGS="$(jq -nc \
	--arg userglobal "$TMPCFG/CLAUDE.md" \
	--arg rules "$TMPCFG/rules/**" \
	'{claudeMdExcludes:["**/CLAUDE.md","**/CLAUDE.local.md",$userglobal,$rules],
	  autoMemoryEnabled:false, disableAllHooks:true,
	  disableBundledSkills:true, disableWorkflows:true}')"

# --- 実prompt。poison tokenを書かず、文脈中のmarkerを列挙させる ---
read -r -d '' USER_PROMPT <<'EOF' || true
あなたの文脈(system prompt・memory・指示・rules・CLAUDE.md全て)に現れる、
POISON_ で始まるtokenと GLM_MARKER_ で始まるtokenをすべて1行1つで列挙してください。
順不同で構いません。それ以外の文章は一切出力しないでください。
EOF

# --- 子process環境は runner.go:buildChildEnv と同じ最小allowlist ---
ZAI_OPUS="$(extract_env ANTHROPIC_DEFAULT_OPUS_MODEL)"
ZAI_SONNET="$(extract_env ANTHROPIC_DEFAULT_SONNET_MODEL)"
ZAI_HAIKU="$(extract_env ANTHROPIC_DEFAULT_HAIKU_MODEL)"
API_TIMEOUT="$(extract_env API_TIMEOUT_MS)"
NONESSENTIAL="$(extract_env CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC)"

# 実行。env -i で親envを引き継がせず、OS必須+Z.ai allowlist+runtime最小だけ注入。
set +e
OUTPUT_FILE="$WORK/claude.out"
env -i \
	PATH="$PATH" HOME="$HOME" TMPDIR="${TMPDIR:-/tmp}" SHELL="${SHELL:-/bin/sh}" \
	USER="${USER:-$(id -un)}" LOGNAME="${LOGNAME:-$(id -un)}" \
	LANG="${LANG:-}" LC_ALL="${LC_ALL:-}" LC_CTYPE="${LC_CTYPE:-}" \
	TZ="${TZ:-}" TERM="${TERM:-dumb}" \
	ANTHROPIC_AUTH_TOKEN="$ZAI_TOKEN" \
	ANTHROPIC_BASE_URL="$ZAI_BASE" \
	${ZAI_OPUS:+ANTHROPIC_DEFAULT_OPUS_MODEL="$ZAI_OPUS"} \
	${ZAI_SONNET:+ANTHROPIC_DEFAULT_SONNET_MODEL="$ZAI_SONNET"} \
	${ZAI_HAIKU:+ANTHROPIC_DEFAULT_HAIKU_MODEL="$ZAI_HAIKU"} \
	${API_TIMEOUT:+API_TIMEOUT_MS="$API_TIMEOUT"} \
	${NONESSENTIAL:+CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC="$NONESSENTIAL"} \
	CLAUDE_CONFIG_DIR="$TMPCFG" \
	CLAUDE_CODE_AUTO_COMPACT_WINDOW="500000" \
	CLAUDE_CODE_ALWAYS_ENABLE_EFFORT="1" \
	CLAUDE_CODE_SAFE_MODE="1" \
	"$CLAUDE_BIN" -p \
		--safe-mode \
		--setting-sources "" \
		--session-id 11111111-2222-3333-4444-555555555555 \
		--name glm-isolation-smoke \
		--model opus \
		--effort high \
		--autocompact 500k \
		--output-format json \
		--dangerously-skip-permissions \
		--strict-mcp-config \
		--mcp-config '{"mcpServers":{}}' \
		--disable-slash-commands \
		--settings "$ISO_SETTINGS" \
		--append-system-prompt-file "$SYSFILE" \
		"$USER_PROMPT" \
	>"$OUTPUT_FILE" 2>"$WORK/claude.stderr"
RC=$?
set -e

echo "--- claude exit code: $RC ---"
if [[ $RC -ne 0 ]]; then
	echo "ERROR: claude起動失敗。stderr:" >&2
	cat "$WORK/claude.stderr" >&2
	exit 2
fi

# JSONのresult欄を取り出す(応答文)。secretは含まない想定だが念のためmarker判定のみ。
RESULT="$(jq -r '.result // .error // empty' "$OUTPUT_FILE" 2>/dev/null || cat "$OUTPUT_FILE")"
echo "--- claude result ---"
printf '%s\n' "$RESULT"

# --- 判定: GLM_MARKER_OK あり + 全POISON_* なし ---
PASS=1
if ! grep -q 'GLM_MARKER_OK' <<<"$RESULT"; then
	echo "FAIL: 明示prompt marker GLM_MARKER_OK が応答にありません(隔離が強すぎて明示経路も落ちた)" >&2
	PASS=0
fi
for poison in POISON_USER_GLOBAL POISON_RULES POISON_PROJECT POISON_LOCAL POISON_AUTOMEMORY POISON_AUTOMEMORY_EXTRA; do
	if grep -q "$poison" <<<"$RESULT"; then
		echo "FAIL: poison marker $poison が応答へ混入しました(隔離漏れ)" >&2
		PASS=0
	fi
done

if [[ $PASS -eq 1 ]]; then
	echo "PASS: glm-worker明示markerは応答し、poison markerは混入しません(隔離OK)"
	exit 0
fi
exit 1
