---
title: pw dev
description: 開発ループ。サービス起動、生成、マイグレーション、CSS、変更時の再起動。
sidebar:
  order: 5
---

```sh
pw dev
```

日常的に使うコマンドです。開発ループはプロジェクトファイルで定義されるため、引数は
取りません。

## 起動時にすること

1. `devbox.json` に宣言された Devbox サービスを起動する
2. [`pw generate`](/ja/pw/project/generate/) を実行する
3. `migration.auto` が `false` でなければ、未適用のマイグレーションを適用する
4. Tailwind が有効なら、スタイルシートをビルドしてウォッチャを起動する
5. `dev.idp.enabled` が `true` なら、開発用の認証プロバイダを起動する
6. `dev.otel.enabled` が `false` でなければ、テレメトリビューアを起動する
7. `dev.logs.enabled` が `false` でなければ、ローカルJSONL保存を準備する
8. `project.main` をビルドして実行する

起動後は 0.5 秒ごとに監視対象を確認します。変更があれば、環境全体ではなく、その
ファイルに関係するステップだけを繰り返します。再起動したアプリケーションは
[設定サマリ](/ja/productivity/startup-summary/#pw-dev-での再起動)を出力し直しません。
設定が変わらなかった再起動は `reloaded` とだけ表示し、変わった再起動は動いた行だけを
表示します。

## 監視する対象

- プロジェクト自身の Go、`.pw.html`、`.pw.sql` のソース
- マイグレーションディレクトリ
- Tailwind が有効な場合はその入力ファイル
- `popcornweb.toml` の `dev.watch.includes` に一致するもの

走査の範囲は `[generate]` の用途ではなくモジュール全体です。どの用途も生成に使わない
ファイルを含め、Go のソースはすべて再ビルドの入力だからです。`.git`、`vendor`、
`node_modules`、`.devbox`、`public` ツリーは常にスキップされます。プロジェクトルートの
下にある `git worktree add` で作られた worktree も同じで、コーディングエージェントが
`.claude/worktrees` に置くチェックアウトがその例です。worktree はソースをもう一組
丸ごと持っているので、監視対象に含めると、別のチェックアウトを編集するたびに
アプリケーションが再起動してしまいます。サブモジュールはプロジェクトの一部なので、
ほかのディレクトリと同じように走査されます。

`dev.watch.includes` は、走査が届かない入力を加えるための相対 glob パターンです。
`dev.watch.excludes` はサブツリーをスキップします。大きな依存ツリーが走査をループ中で
最も遅いステップにしてしまうときに使います。どちらも絶対パスは拒否されます。

```toml
[dev.watch]
includes = ["config.dev.toml", "assets/**/*.svg"]
excludes = ["web/node_modules"]
```

## ポート

アプリケーションは `server.port` を bind しますが、開発ではそこに固執しません。別の
ターミナルでもう一つプロジェクトが動いている、正常に終わらなかったループがプロセスを
残している——そうしてポートが埋まっていたとき、実行は終わらずに次の空きポートへ移り、
どこへ移ったかを言います。

```
WARN the configured port could not be bound, so this development run moved to the next free one
     configured_port=8080 port=8081

listening on http://localhost:8081
```

数字は 2 つ出ますが、意味は別です。設定ツリーの `server.port` は設定ファイルが要求した
値で、最後の `listening` 行は実際に応答するアドレスです。ブラウザで開くのは後者です。
コンソールがリンクするのはアプリケーション自身が申告したアドレスであってプロジェクト
ファイルの値ではないので、コンソールのアプリケーションリンクもシフトに追随します。

探索は設定されたポートから 10 個先で打ち切ります。そしてこれを行うのは開発時の実行だけ
です。`APP_ENV=stg` も `APP_ENV=prod` も、名前のついた環境はすべて設定どおりのポートを
bind し、できなければ失敗します。ヘルスチェックもリバースプロキシも運用者も、設定
ファイルが指すポートに来るからです。ただし `APP_ENV` 未設定は development に解決される
ので、変数を一度も設定していないデプロイもシフトしえます。警告が環境名を書くのはその
ためで、`APP_ENV` を設定することが厳密な bind を取り戻す方法です。

開発でもポートを固定したいとき——コールバック URL を登録済みの外部 OAuth プロバイダを
使うときなど——ポートが空いてさえいればシフトは起きないので、対処は塞いでいる相手を
止めることになります。[`pw doctor`](/ja/pw/project/doctor/) はループを起動する前に
`server.port` が埋まっていることを報告します。後から警告を読むより早く気づけます。

## サービス

`devbox.json` に宣言されたサービス（既定では Valkey）は、Devbox のプロセスマネージャの
全画面 TUI を無効にした状態で動きます。ログは画面を覆い隠すのではなく、コード生成・
マイグレーション・アプリケーションの出力と同じストリームに、サービス名つきの 1 行ずつ
流れます。

```
[valkey	] 1:M 27 Jul 2026 23:02:32.103 * Ready to accept connections tcp
```

サービスが不要なプロジェクトは `devbox.json` からパッケージを外してください。`pw dev` が
起動するのは Devbox が宣言したものだけです。

## Tailwind

開発中のウォッチャは `assets.tailwind.minify` の設定に関わらず、常に**非 minify** の
CSS を作ります。minify がループの中で最も遅い部分だからです。CSS ウォッチャが失敗しても
サーバーは停止しません。`pw dev` は動き続け、入力ファイルを直接監視する方式へ
フォールバックします。

`tailwindcss` は `PATH` 上にある必要があります。そのための `devbox shell` です。
[スタイリング](/ja/guides/frontend/styling/)を参照。

## マイグレーション

未適用のマイグレーションはアプリケーションの起動前に適用され、マイグレーション
ディレクトリのファイルが変わったときにも適用されます。自分で制御したい場合は無効に
できます。

```toml
[migration]
auto = false
```

## 開発用の認証プロバイダ

`dev.idp.enabled` が true のとき、`pw dev` はローカルの OpenID Provider を起動し、
issuer と資格情報をアプリケーションのプロセスに注入し、ループと一緒に停止します。
ユーザー定義ファイルを編集すると、再起動なしでリロードされます。

```toml
[dev.idp]
enabled = true
```

ユーザー定義ファイルの形式、claim、このプロバイダが実装するもの・しないものは
[開発用の認証プロバイダ](/ja/productivity/dev-identity-provider/)を参照してください。

## テレメトリビューア

`pw dev` はループバックの OpenTelemetry レシーバとブラウザ UI も起動し、標準の OTLP
環境変数でアプリケーションをそこへ向けます。既定で有効なので、コレクタを用意しなくても
トレースと関連づいたログレコードが読めます。

```
pw dev: telemetry viewer http://127.0.0.1:54321
```

```toml
[dev.otel]
enabled = true
```

`OTEL_EXPORTER_OTLP_ENDPOINT` がすでに設定されていれば何も起動せず、自前のコレクタに
任せます。[開発用テレメトリビューア](/ja/productivity/dev-telemetry-viewer/)を参照して
ください。

## ローカル構造化ログ

アプリケーションログは読みやすいテキストとしてターミナルへ出続けます。ビューアとは
独立して、`pw dev` は構造化形式も `.log/pw-dev-*.jsonl` へ保存します。一回の起動が
再ビルドをまたいで一つのファイルを所有し、ファイルは最初のレコードで初めて現れます。

```toml
[dev.logs]
enabled = true
directory = ".log"
```

ディレクトリはプロジェクトからの相対パスで、自動削除されません。このスイッチを無効に
すると、ターミナルと設定済みOTLP出力だけを残せます。スキーマとDuckDBクエリは
[テレメトリ](/ja/guides/architecture/telemetry/)を参照してください。

## コンソール

`pw dev` はアプリケーションと並んで、固定のループバックポートでブラウザ向けの
コンソールを配信します。ループに必要なペインがここに集まります。プロジェクトの状態、
静的ファイル、データベースと宣言済みクエリ、テンプレートの storybook、`pw doctor`、
そして上記のテレメトリビューアです。

```
pw dev: console http://127.0.0.1:18081
```

```toml
[dev.console]
enabled = true
port = 18081
```

ループの他の部分はここを通して読みます。そしてそのどれもリリースビルドには存在
しません。[開発コンソール](/ja/productivity/dev-console/)を参照してください。

## テストデータエンドポイント

`pw dev` がビルドするアプリケーションは `pwdev` ビルドタグ付きで、開発環境ではその
バイナリが自身のリスナーで `POST /_pw/test/seed/{dataset}` と
`GET /_pw/test/assert/{dataset}` を、ループバックからの呼び出しに限って提供します。
ブラウザテストスイートはこれを使い、`pw seed` が読むのと同じ `testdata/seed` の
ファイルでデータベースを初期化・検証します。リリースビルドはエンドポイントのバイト列を
持ちません。[E2E テスト](/ja/productivity/e2e-testing/)を参照してください。

## 停止

`Ctrl-C` はループ全体をキャンセルし、アプリケーション、Tailwind のウォッチャ、
Devbox のサービスを停止します。ループを終わらせるのはこれだけです。アプリケーションが
自分で終了した場合——コンパイルエラーでも、パニックでも、正常終了でも——`pw dev` は
`application exited: …` と報告したうえで監視を続けます。動く状態から次の動く状態までの
間、プロジェクトはたいてい動かない状態にあるからです。次に保存した変更が、再ビルドと
再起動を行います。
