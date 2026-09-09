---
title: pw doctor
description: 指定した環境で有効になる機能や依存関係、設定の問題を、アプリケーションを起動せずに診断します。
sidebar:
  order: 7
---

```sh
pw doctor
pw doctor --env=prod
```

Popcorn Web のアプリケーションが「何を動かすか」は、単一のファイルには書かれて
いません。設定ファイルはセッションバックエンド、データベース、認証プロバイダを
**選び**ます。それを実装するコードがバイナリに入っているかどうかを決めるのは
import です。プラグインはブランクインポートで自分を登録するからです。片方ずつは
正しくても、その隙間でプロジェクトは壊れます。`session.backend = "rdb"` と書かれた
ファイルと、`rdb` を登録するプラグインを import していないアプリケーション。

起動時検証はこれを捕まえます。ただし捕まえるのは本番で、プロセスがリクエストを
受け付けなくなった瞬間に、問題は名指ししても直し方の import 行は書かれていない
メッセージで、です。`pw doctor` は同じ問いに、もっと早く、しかもデプロイ先では
ないマシンから答えます。

## 環境は引数であって、シェルの状態ではない

デプロイ設定が妥当かどうかの確認は、デプロイ**前**にしか意味がありません。だから
診断する環境はオプションです。

| コマンド | 診断対象 |
| --- | --- |
| `pw doctor` | シェルの `APP_ENV`、未設定なら `dev` |
| `pw doctor --env=prod` | `config.prod.toml`。このホストの環境変数の下に `.env`、`.env.local`、`.env.prod`、`.env.prod.local` を重ねる |
| `pw doctor --env=stg --env=prod` | 両方を 1 つのレポートに |
| `pw doctor --env=all` | プロジェクトにある `config.*.toml` すべて |

このオプションが変えるのは読むファイルだけです。アプリケーションプロセスには
届かないので、`APP_ENV` を上書きするものは何もありません。

この区別は実際に働きます。深刻度は「本番はこうあるべき」という固定観念ではなく、
診断対象の環境に従うからです。クエリログが有効なのは `dev` では想定どおりの姿で、
それ以外では閾値とバインド値の有無を名指しする警告になります。`secure` のない
セッションクッキーは `dev` では note、デプロイ環境では error です。ループバックの
OIDC issuer は [`pw dev`](/ja/pw/project/dev/) がローカルでログインさせる仕組み
そのものであり、ステージングでは障害か、それ以上のものです。

同じファイルを 2 回判定するのは、同じファイルが場所によって違う意味を持つから
です。

```
$ pw doctor --env=dev
0 error, 0 warning, 3 note

$ pw doctor --env=prod
6 error, 4 warning, 2 note
```

## 何を読み、何をしないか

`pw doctor` はプロジェクトを読みます。アプリケーションをビルドせず、プロセスを
起動せず、ファイルを書きません。接続も、こちらが求めない限り開きません。

これは慎重さのための慎重さではありません。診断が最も欲しくなるのはアプリケーション
がコンパイルできなくなった時であり、先にビルドを要求するツールはまさにその時に
何も言えません。ラップトップから本番設定を診断するのに本番へ触れる必要はなく、
本番の秘密情報がラップトップに置かれている必要もありません。

読むのは次のものです。

- `popcornweb.toml`、マイグレーションディレクトリ、`devbox.json`、`go.mod`、
  生成物、そしてリポジトリが追跡・無視しているもの
- その環境が選ぶ設定ファイルを型付きデフォルトにマージした結果と、各キーを
  勝ち取った層
- 自分自身のプロセス環境（そう明示した上で）
- `go list` で解決した `project.main` の import グラフ。プラグインやドライバの
  import 漏れが、ビルドなしで見えるのはこれのおかげです

レポートは findings の前に状態を出します。findings は、それを生んだ値の隣でしか
読めないからです。

```
features
  database             on  sqlite
  session              on  rdb  not linked: github.com/shibukawa/popcornweb/sessionstore/sqlite
  authentication       off
  security headers     on
  query diagnostics    on  auto

middleware, in order
  1. recovery
  2. request id
  3. access log
  4. database pool
  5. session
  6. application handler

database
  default#1  sqlite  default, write
```

## 指摘には ID と直し方が付く

```
  error PW0402: connection default#1 uses the mysql scheme and the application links no driver for it
        add: import _ "github.com/shibukawa/tinygodriver/database/sql/mysql"
        fix: add the blank import of the driver package for that scheme
        …/appendix/diagnostics/#pw0402-no-database-sql-driver-answers-the-configured-dsn-scheme
```

ID は安定していて再利用されません。だから `PW0402` は検索でき、issue に貼れ、
[診断リファレンス](/ja/appendix/diagnostics/)（英語）で引けます。このページはコマンドが
評価するのと同じカタログから生成されるので、エントリのないチェックは存在でき
ません。

秘密情報は**場所**で報告され、値では報告されません。指摘が名指しするのはキーと
ファイルで、資格情報そのものはレポートのどのセクションにも出ません。DSN は運用上の
事実である半分 — `postgres://*****@db.internal:5432/app` — を残すので、その環境が
どのデータベースに繋がっているかはレポートから読めます。`--env=all` を
付ければ、2 つの環境ファイルに現れた同一のリテラル秘密値が、双方のキーの一致
として報告されます。これは他の何にも見えません。動いているプロセスは自分の環境は
知っていても、別環境のファイルは知らないからです。

分類はフィールド名によるので、DSN はすべて秘密扱いになります。`sqlite://app.db`
は資格情報を持たないため、開示の指摘にはなりません。読み飛ばすことを覚えた警告は、
本当に効く警告を巻き添えにします。

秘密を**どこに置いているか**は配備の問題なので、`--env=dev` では問いません。
Devbox がアプリケーションの隣で動かすデータベースのパスワードは `config.dev.toml` に
あってよく、そのファイルはチームで共有する前提で書かれています。同じ内容が
`--env=prod` なら error ですし、そのファイルをコミットしていることも error です。
秘密が**何であるか**はどの環境でも判定します。scaffold のプレースホルダのまま残った値や、
配備環境と共有している値は `dev` でも指摘されます。

## 見なかったもの

レポートの最後には、この実行が判定できなかったものと、そのせいで走らなかった
チェックが並びます。

```
not examined
  database: --online was not given, so nothing was contacted
    applied migration state and connection reachability were not read
  environment variables: this host does not hold a deployment's environment
    a key whose deployed value arrives from the environment is reported as unknown at this host
```

ラップトップでのクリーンなレポートと CI でのクリーンなレポートは、同じ主張では
ありません。どちらを手にしているかはレポート自身が述べます。`prod` で認証が有効
なのに、このホストから読める場所に provider の値が 1 つも宣言されていない場合、
それはプラットフォームによる注入か、本物の欠落かのどちらかです。`pw doctor` には
区別できないので、推測せずに「デプロイ側が設定すべき環境変数」を名指しします。

同じコマンドを、その変数が実在する CI で走らせれば、note は判定に変わります。

```sh
pw doctor --env=prod --strict --format=json
```

## オプション

| オプション | 効果 |
| --- | --- |
| `--env=<token>` | 診断する環境。複数指定可、`all` も可 |
| `--config-path=<path>` | 明示した 1 ファイルを診断する |
| `--format=text\|json` | CI 向けは `json`。キー名はサポートされたインタフェース |
| `--strict` | warning も失敗として扱う |
| `--online` | データベース接続を許可する（到達性と未適用マイグレーション） |

`--online` なしでは、マイグレーションのセクションは未適用件数を「不明」と述べます。
接触していないデータベースを、きれいに見せることはしません。付けた場合は
[`pw migrate`](/ja/pw/database/migrate/) が持つのと同じドライバリンクで接続し、
何も適用せず、存在しない SQLite ファイルは開きません。開けば作ってしまいますし、
書き込む診断は診断ではないからです。

## 終了ステータス

問題がなければ `0`、error の指摘があれば `1`、`--strict` 下では warning でも `1`
です。

## `--fix` はない

意図的にありません。信用する前に監査が必要な診断は、読める診断より価値が低い
です。代わりに、すべての指摘が解決するコマンドを名指しします。そのコマンドは
すでに存在します。設定と依存が食い違った機能には
[`pw add`](/ja/pw/project/add/)、ソースより長生きした生成ファイルには
[`pw generate`](/ja/pw/project/generate/)、ソースに追いついていないスキーマには
[`pw migrate`](/ja/pw/database/migrate/) です。
