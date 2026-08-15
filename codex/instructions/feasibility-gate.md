# 外部成立性のfeasibility gate

外部service・取得方式・実行環境等の外部成立性が本番設計の前提になる変更をGLMへ委譲する場合と、その完了報告を受け取る場合に適用する。親Codexのorchestration contractであり、worker/reviewerへの個別checklist追加で代替しない。

## 適用条件

- 未検証のcritical assumption(外部serviceの継続提供、取得方式の継続成立、実行環境からの到達・認証成立等)がproduction code・IaC・運用展開の設計前提になり、誤った前提のまま進んだ後続コストが大きい場合だけgateを適用する。
- 通常の局所変更、確立済み前提の範囲内変更、短時間の意味的検証でcritical assumptionを解消できる対象へ、形式的なPoCや固定の観測期間を要求しない。

## gateで固定する内容

production実装へ進む前に、次を対象の不確実性・変動性・継続成立性の重要度に応じて明示する。全対象へ同じ深度を機械的に要求しない。

- 未検証のcritical assumptionの列挙
- assumptionごとの最小PoCと代表case
- 意味的成功条件: 必要データの意味的検証と代表caseのterminal outcomeまでを含める。HTTP 200・process exit 0・単発取得等のtransport成功だけを成立性の証明にしない
- 必要な試行回数・観測期間: 対象固有の不確実性と変動性から決める。Amazon取得PoCの48〜72時間はその対象固有の観測条件であり一般contractへ固定しない。外部API schema確認・実行環境からの到達確認・認証方式の成立確認など短時間の意味的検証で足りる対象へ長時間試験を要求しない
- Go/No-Go基準と撤退条件

## orchestration

- 成立性検証のPoC・観測taskとproduction実装taskを分離する。未検証の外部成立性を前提にしたproduction code・IaC・運用展開の実装をGLMへ委譲しない。
- transport成功だけの完了報告を成立性の証明として受領しない。意味的成功条件・代表caseのterminal outcome・観測結果が揃わない完了報告は差し戻す。
- Go/No-Goと撤退判断はSol High・ユーザーへ戻し、GLMだけで確定させない。
- 観測中に前提が崩れた場合は、workaroundの追加実装をさせず観測事実をSol/ユーザー判断へ戻す。
- 単発の具体的成功を継続運用可能性へ一般化しない。同時に個別PoCの長時間観測条件を全feasibility gateへ一般化しない。
