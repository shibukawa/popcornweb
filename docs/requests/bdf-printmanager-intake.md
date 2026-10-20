# BDF Print Managerからの依頼

[依頼原文](bdf-printmanager-public-assets.md)を `source:bdf-printmanager-request` として登録しました。
対象として報告されたバージョンは popcornweb v0.5.11、依頼日は2026-10-21です。
原文は英語のまま保存しています。

取り込みの起点は [`concept:bdf-printmanager-integration`](../../.knowledge/concepts/concept/bdf-printmanager-integration.yaml) です。
取り込んだ6件は実装済みで、24件の受け入れ条件に検証結果を記録しています。
変更点と検証範囲は[実装・検証結果](bdf-printmanager-implementation.md)を参照してください。
BDFの実環境での再現試験は行っていません。

| 優先度 | 項目 | 要件 |
| --- | --- | --- |
| 最優先 | モジュール配下のアプリに合わせて `assets.public`・`assets.derived`・`assets.embed` を設定可能にする。既定値は維持する | [configurable-public-asset-locations](../../.knowledge/concepts/requirement/configurable-public-asset-locations.yaml) |
| 次点 | 開発時の派生ツリー欠落を可視化する。依頼元の希望は埋め込みFSへのフォールバックと診断 | [development-public-asset-fallback](../../.knowledge/concepts/requirement/development-public-asset-fallback.yaml) |
| 低 | 設定したアセット配置でパッケージングし、環境変数だけで動くアプリは `config.prod.toml` を省略可能にする | [optional-production-config-packaging](../../.knowledge/concepts/requirement/optional-production-config-packaging.yaml) |
| 非ブロッキング・不具合報告あり | `[]string` の既定値とCSRFのinclude既定値を整合させ、宣言方法を文書化する | [string-slice-config-defaults](../../.knowledge/concepts/requirement/string-slice-config-defaults.yaml) |
| 非ブロッキング | `pw.Run` より先にリソースを開くアプリ向けに、設定解析とフレームワークアクションの処理順を文書化する | [early-framework-action-dispatch](../../.knowledge/concepts/requirement/early-framework-action-dispatch.yaml) |
| 非ブロッキング | クエリがないアプリでも `generate.queries = []` が必要なことを文書化する | [empty-query-scope-documentation](../../.knowledge/concepts/requirement/empty-query-scope-documentation.yaml) |

アセット配置の受け入れ条件は、ルートが `package fixture`、アプリが `cmd/app` にあるモジュールでの生成・開発配信・ビルドです。
ルートに不要なアセットを生成せず、設定を省略した既存fixtureの出力がバイト単位で変わらないことも含めています。

開発時のフォールバックに合わせ、`decision:development-public-assets` と
`decision:derived-tree-development` にある「ローカルに無ければ404」の判断を更新しました。
取り込み時に保持した旧判断は履歴として残し、派生ツリーとフォールバックの判断を更新しました。パス検証やファイル種別の検証による拒否を
通常のファイル欠落として扱わない条件も、既存の安全性要件から引き継いでいます。

採用した方式は、`server.public.local_root` と環境変数によるパスの引き渡し、
派生ツリーの隣への補助生成物の配置、埋め込みFSへのフォールバックと起動時診断です。
本番設定ファイルは無ければ省略し、スライス既定値はカンマ区切りに統一して配列風の宣言を生成時に拒否します。
対応する未決事項には採用内容と根拠を記録しています。

```sh
skill_dir="${CODEX_HOME:-$HOME/.codex}/skills/knowledge-memory"
python3 "$skill_dir/scripts/knowledge.py" --project . show concept:bdf-printmanager-integration
python3 "$skill_dir/scripts/knowledge.py" --project . show requirement:configurable-public-asset-locations --facet acceptance
python3 "$skill_dir/scripts/knowledge.py" --project . show requirement:development-public-asset-fallback --facet open_questions
```
