# 永続状態の状態遷移品質ゲート
永続状態・設定・migration・upgrade・cache・manifest・sidecar・local fileに関わる変更は、意味変更の意図の有無に関わらず時間軸上の意味ある状態遷移を選定し検証する。全変更で全遷移を機械実行せず、この変更が実際に影響する遷移だけを選び、選定理由と検証結果を確認する。高リスク扱いは永続状態の意味変更・migration要否・既存形式やユーザー状態との互換・rollback/recovery・upgrade破壊可能性で意味判断が必要な場合だけに限定する。

## 遷移の選定
変更内容に応じて意味あるものだけ選ぶ。
- fresh導入 / 既存環境への再実行 / 旧versionからのupgrade
- 追加・変更・削除、有効化・無効化、override適用・override解除
- 開始状態としての既存state / 欠損 / 破損 / 部分消失
- 途中失敗、rollback・retry・resume・recovery
- 永続識別子・pathのrename
- 既存ユーザーデータ・設定の保持

## 設定・override・managed file・migrationの独立観点
- 適用後に変更前の意味的状態へ戻れるか。追加物がoverride解除や削除後に残留しないか。
- 削除した値が親環境・別経路から再流入しないか。
- null・空・欠損入力が黙って無視(silent no-op)されず、意味に応じてfailしtargetを書き換えないか。
- baselineを現在値(変更後)から誤再構築していないか。欠損・部分消失stateを安易に安全復旧扱いしないか。

## 永続識別子の変更
既存導入済み環境からのupgrade、migration要否、旧識別子維持が単純かを比較する。永続識別子のrenameは既存環境を見落としやすく、維持が単純ならそちらを優先する。

## recovery手順
README・error文のrecovery手順は概念上またはtestで検証する。未検証のものを安全と表現しない。

## PACKET報告
状態遷移の結果(変更前後・upgrade・disable/remove・recovery・互換懸念)は新fieldを増やさず既存field(`SUMMARY`・`INVARIANTS`・`UNVERIFIED`・`RESIDUAL_RISK`等)へ短く圧縮する。schema拡張・packet長大化は本当に必要な場合だけ。
