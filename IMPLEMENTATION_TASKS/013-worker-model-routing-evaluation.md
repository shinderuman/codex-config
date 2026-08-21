# Task: worker model routingを評価可能にする

## Status

planned

## Original instruction

GLM-4.7等はsample不足のため、実運用データが揃うまで品質証拠なしのdowngradeをしない。ユーザー許可のないbenchmark目的追加callは禁止する。

## Amendments

- 2026-08-22 parent maintenance:

````text
## 12. `tree usage`等の未定義metricを勝手に増やさない

Task 013やblocked A/B等に、既存schema上の意味が明確でないmetric名がある場合は確認してください。

特に`tree usage`が現在のtelemetry/Evalで具体的な定義を持つか確認すること。

既存metricなら正しい名称・定義をHistorical invariantへ紐付ける。

存在しない/曖昧なら、このtaskのためだけに新telemetryを増やさず削除または既存usage metricへ置換してください。

名前だけから新しい観測機能を実装しないでください。
````

## Purpose

Codex/GLM costとQuality Deltaを実データで比較できるようにする。

## Contract

- alias、resolved model、role、phase、quality outcomeと、current ModelCallLog v3の`tree_usage`を比較
- `tree_usage`は`resolved_model_usage`各modelのinput/cache-creation/cache-read/output token合計、同mapが空なら`top_level_usage`へfallbackする既存定義を使う。別metricを新設せず、取得不能ならunknownとする
- routing変更は別blocked判断へ渡す

## Must not

- sample不足でdowngrade、無許可benchmarkを行わない

## Acceptance criteria

- sample sufficiencyと評価metricを定義
- 現dataをunknownとして正しく表示
- 独立reviewer、risk/contractに応じて必要なSol品質gate、commit

## Historical invariants

- 2026-08-21 GLM-4.7 sample 6 call tree
- `glm-worker/internal/state/telemetry.go`のModelCallLog v3 `tree_usage` / `modelCallTreeUsage()`定義

## Dependencies

- 009-worker-call-outliers.md
- 011-operation-category-telemetry.md

## Review findings

none

## Current boundary

依存data待ち。
