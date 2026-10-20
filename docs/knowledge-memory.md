# Knowledge Memory

このリポジトリの `.knowledge` は knowledge-memory 形式（version 1）です。
knowledge-compiler 形式の703件を移行し、ID・タイトル・本文・解析できた構造化フィールドを保持しています。

## 保存場所

- `config.yaml`: 形式のバージョン。
- `sources/`: 手書きの概念を読む `source:authored`、BDFの依頼原文を読む `source:bdf-printmanager-request`、14個のGoモジュールの依存宣言を読む `source:go-modules` を登録しています。
- `concepts/<type>/<name>.yaml`: 概念。既存の `type:name` IDを引き続き使います。
- `schema/`: 現在の形式を検証するJSON Schema。
- `_schema/`: 旧形式の型定義。移行元のメタデータとして保持しており、現在の検証には使いません。
- `.cache/`: ローカルのDuckDB索引・移行バックアップ。Git管理対象外です。

BDFからの依頼は [取り込み一覧](requests/bdf-printmanager-intake.md) から参照できます。

## 検索と更新

Python 3.11以降、DuckDB CLI 1.5以降、および `knowledge-memory` スキルが必要です。
依存Pythonライブラリはスキルに同梱されています。以下の `skill_dir` はインストール先に合わせます。

```sh
skill_dir="${CODEX_HOME:-$HOME/.codex}/skills/knowledge-memory"
python3 "$skill_dir/scripts/knowledge.py" --project . sources
python3 "$skill_dir/scripts/knowledge.py" --project . search "public assets" --limit 5
python3 "$skill_dir/scripts/knowledge.py" --project . show flow:derived-asset-build --facet inputs
python3 "$skill_dir/scripts/knowledge.py" --project . neighbors flow:derived-asset-build --direction both --depth 1 --limit 10
```

概念を編集したら、保存データの検証と索引の再構築を行います。

```sh
python3 "$skill_dir/scripts/knowledge.py" --project . compile
python3 "$skill_dir/scripts/knowledge.py" --project . doctor
```

`compile` はコードの収集やテストを実行しません。検索結果は保存された記述であり、
実装が現在も一致することや、記載されたテストが通ることを保証するものではありません。

## 移行で保持した診断

96件に、旧Markdown内のYAMLを構造化できない診断があります。
該当部分を推測で修正せず、`facts.legacy_body` と `facts.legacy_parse_issues` に保持しています。
一覧は [移行診断レポート](knowledge-exports/knowledge-memory-migration.json) を参照してください。
原文も検索対象であり、必要なときだけ明示的に取得できます。

```sh
python3 "$skill_dir/scripts/knowledge.py" --project . show api:cli-dev --facet legacy_body --max-chars 8000
python3 "$skill_dir/scripts/knowledge.py" --project . show api:cli-dev --facet legacy_parse_issues
```

構造化の修復は該当する概念を更新するときに行い、原文との意味の一致を確認します。
移行時の原本は `.knowledge/.cache/migration-backups/` にあり、移行前の内容はGitの履歴でも確認できます。
