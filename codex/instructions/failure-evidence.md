# 原因不明runtime failureの最小evidence管理

外部取得・parser・integration failureを含む依頼をGLMへ委譲する場合と、その完了報告を受け取る場合に適用する。親Codexのorchestration contractであり、worker/reviewerへの個別checklist追加で代替しない。

## 適用条件

- 外部取得・parser・integration failureで、HTTP status・size・error分類のような要約だけでは根本原因または再現条件を判定できず、response本文・header・payload断片・parser入力等の実物が診断に必要な場合だけ、evidenceをtask artifactへ保存させる。
- 通常の十分診断可能なerror、成功応答、局所bugへ形式的なartifact保存を要求しない。全response・全成功応答の無条件保存はしない。

## 保存させるevidence

- 再現に必要な最小範囲だけを切り出させ、巨大payloadや診断に不要な部分を保存させない。
- 保存前にcredential・token・cookie・session ID・個人情報等を除去または置換させる。秘密情報を生のまま保存させない。
- 容量上限・retention/削除時期・access範囲を対象リスクに応じて明示する。診断に不要な長期保存をさせない。
- 保存先は既存のtask artifact(`REPORT_ARTIFACT_DIR`・`ARTIFACTS`)だけとし、新しいstorageやtelemetry schemaを作らない。telemetryへ本文を混入させない。

## orchestration

- 委譲前に、必要証拠・sanitization・保存先・retentionをtask固有条件としてUSER_REQUESTへ構成する。一般checklistをworker/reviewer promptへ追加しない。
- artifact保存失敗はbest-effort warningとしてpacketへ残させ、それだけでは本taskを失敗させない。
- 原因判定に証拠が必須なのに取得不能な場合は「判定不能」としてSol/ユーザーへ戻し、推測で修正を重ねさせない。
- packet受理時は`ARTIFACTS`参照先を診断に必要な範囲だけ確認し、全内容をpacketや会話へ転載しない。
