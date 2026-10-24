---
title: pw build
description: 生成コードと準備済みアセットを含む通常または provider 向けリリース成果物を作る。
sidebar:
  order: 6
---

```sh
pw build [--debug] [--backend nethttp|fasthttp]
         [--target lambda|azure-functions|google-cloud-run-functions|vercel-go]
```

現在のプロジェクト状態をリリース成果物にします。target を省略すると通常のバイナリを
生成します。`--backend` は HTTP 実装を選び、既定値は `nethttp` です。`--target` は
provider packaging を選びます。

## 実行内容

1. テンプレート、SQL、ページツリー、カタログ、バインディングの呼び出し箇所を
   ソースの隣の `_pw_gen.go` にコンパイルする
2. Tailwind が有効なら、スタイルシートを **minify して**ビルドする。これは
   `assets.tailwind.minify` を上書きするので、リリースが誤って非 minify になることは
   ない
3. [アセットツリー](/ja/guides/frontend/static-assets/)を `dist/public` に構築する。
   プロジェクトが指定した変換を行い、`*.br`, `*.zstd`, `*.gz` サイドカーを書き、
   キャッシュヘッダを決めるマニフェストを出力する
4. `project.main` が開発専用パッケージに依存していればビルドを拒否する
5. `popcornweb.toml` の `project.main` に対して `go build` を実行する

手順 1 から 4 はそのまま [`pw generate`](/ja/pw/project/generate/) です。この
コマンドは「それ＋コンパイラ」として定義されているので、内容も順序もずれることが
ありません。

バイナリは main パッケージ名でプロジェクトルートに置かれます。スキャフォールドされた
`.gitignore` はこれと、`dist/` 配下すべてを除外済みです。ビルド済みツリーも変換キャッシュも
マニフェストも、すべてビルド成果物です。ルートにその名前のディレクトリがすでにあるプロジェクトは、
[`build.output`](/ja/reference/build-configuration/#build) で別のパスを指定します。指定がなければ、
そのビルドは生成の前に止まってそう伝えます。

`--target` を指定した場合は `.pw/build/<target>/<backend>/` に生成します。Lambda と
Azure Functions は Linux binary と provider metadata、Google Cloud Run functions と
Vercel Go はローカル compile 済みの vendored source tree です。各成果物の契約は
[サーバーレスホスティング](/ja/guides/deployment/serverless/)を参照してください。

現時点で開発専用パッケージは `contrib/devidp` だけです。これは
[`pw dev`](/ja/pw/project/dev/) が使う認証プロバイダで、パスワードを検証せずに
ユーザーをログインさせます。この動作が配布用バイナリにリンクされるのは本番設定ではなく
ビルドの欠陥なので、`pw build` は import しているパッケージ名を示して停止します。

## デバッグ用の成果物

```sh
pw build --debug
```

`--debug` は、配布用の成果物が本来落とすデバッグ情報を残します。スクリプトビルドが出す
ソースマップと、`-ldflags=-s -w` が削る DWARF・シンボルテーブルです。それ以外は変わりません。

使いどころは、複数人でデバッグする共有テスト環境や CD 環境です。staging には使わないで
ください。staging は本番のリハーサルのために存在するので、本番と違う成果物では何の
リハーサルにもなりません。

付けない場合、マップは出力されず、バンドルの `sourceMappingURL` コメントも書かれません。
ツリーに無いマップを名指すバンドルは、devtools を開くたびに存在しないファイルへの
リクエストを生むからです。ハッシュ付きのバンドル名は両者で同じなので、ページが読み込む
URL はどちらで作ったかに依存しません。panic のスタックが関数名と行番号を保つのも両方
同じです。`pw build` は pclntab をどちらでも残します。

`--debug` は [`pw dev`](/ja/pw/project/dev/) のものを何も戻しません。エラーオーバーレイ、
ランチャー、開発用認証プロバイダは、どちらの形の `pw build` 成果物にも入っていませんし、
上の手順 4 はそれらを import したビルドを拒否し続けます。

## 実行する

```sh
APP_ENV=prod ./myapp
```

`APP_ENV` がどのプロジェクトローカル設定ファイルを読むかを選びます。
[アプリケーション設定](/ja/guides/architecture/configuration/)を参照。

## クロスコンパイルと TinyGo

`pw build` は `go build` を呼び出すだけなので、通常の環境変数がそのまま使えます。

```sh
GOOS=linux GOARCH=amd64 pw build
```

生成コードの経路は実行時リフレクションを使わないため、同じソースで TinyGo も
ターゲットにできます。`pw build` は必ずホストの `go` でリンクするので、TinyGo の
ビルドは生成を走らせてからそのコンパイラを自分で呼びます。

```sh
pw generate
tinygo build -scheduler=threads -o myapp ./cmd/myapp
```

[`pw generate`](/ja/pw/project/generate/) はこのコマンドから最後の手順を引いた
ものです。コンパイラが必要とするツリーを一式残し、`public.go` が `go:embed` で
名指ししている `dist/public` もそこに含まれます。ここで `--code-only` を付けて
狭めないでください。そのフラグが書くのは生成 Go だけで、一度も作られなかった
ディレクトリでコンパイラが失敗します。

`-scheduler=threads` はネットワークプロトコルを話すエンジンには必須です。協調型
スケジューラの下ではブロッキングなソケット呼び出しがランタイム全体を掴み、ドライバの
キャンセル監視が動かないため、クエリはコンテキストのデッドラインを越えても何も
報告しません。`database/postgres` と `database/mysql` は、実行時にそうなるかわりに、
このフラグが無ければコンパイルを拒否します。

`pw init` が書く 2 つの Dockerfile は、この両方のコマンドを使います。
[コンテナイメージ](/ja/guides/deployment/container-images/)を参照してください。

TinyGo の `net` パッケージは独自のネットワーク実装を持たず、すべてのソケットが
プログラムの登録した Netdever を経由します。TinyGo サポートを有効にして作成した
プロジェクトには、その登録を行う `tinygohelper.go` がルートに置かれます。

```go
//go:build tinygo

package publicassets

import _ "github.com/shibukawa/tinygodriver/netdev"
```

`//go:build tinygo` により、ホストの Go ビルドではこのファイルは無視されます。
これがないと TinyGo のビルド自体は成功しますが、起動直後に終了します。

```
2026/01/01 00:00:00 Netdev not set
```

このファイルが付くのは `--tinygo`、またはウィザードで TinyGo を選んで作成した
プロジェクトだけで、既定では付きません。TinyGo に切り替えるときは手動で追加して
ください。

## CI での使い方

ビルド前に、生成コードが最新であることを検証します。

```sh
pw check
pw build
```
