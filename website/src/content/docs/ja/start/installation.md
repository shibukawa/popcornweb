---
title: インストール
description: pw コマンドを Homebrew、Nix、リリースアーカイブ、Go ツールチェインのいずれかで導入し、Popcorn Web を Go モジュールに追加する。
sidebar:
  order: 1
---

`pw` コマンドは、スキャフォールド、コード生成、マイグレーション、開発サーバーを
まとめて扱います。まず `pw` をインストールしてください。`pw init` で作る
プロジェクトは Go のツールチェーンを固定するため、プロジェクト作成前に別途 Go を
インストールする必要はありません。

## `pw` を入れる

### Homebrew

```sh
brew install shibukawa/tap/pw
```

タグ付きリリースのビルド済みバイナリを入れます。対象は macOS（Apple Silicon と Intel）と
Linux です。更新は `brew upgrade` で行えます。

### Nix

```sh
nix run github:shibukawa/popcornweb#pw -- version
```

これは何もインストールせずに `pw` を実行します。`PATH` に置きたい場合は、flake の
`packages.<system>.pw` をプロファイルや環境に追加するか、公開されている
`overlays.default` を自分の flake から使ってください。

derivation は `buildGoModule` でソースからビルドするので、`x86_64-linux`、
`aarch64-linux`、`aarch64-darwin` を対象にします。Intel の macOS は Homebrew の formula と
リリースアーカイブが担当します。nixpkgs がそのプラットフォームを外したためです。

flake は Go、`gopls`、TinyGo を含む `devShells.default` も公開しています。Devbox を
使わずにホスト側のツールチェインが欲しいときに使えます。

### リリースアーカイブ

タグごとに、ターゲット別のアーカイブと `checksums.txt` が
[リリースページ](https://github.com/shibukawa/popcornweb/releases)に公開されます。
展開するとディレクトリの階層無しで `pw` が出てくるので、チェックサムを検証してバイナリを
`PATH` に置けば終わりです。Windows をカバーするのは、このチャネルだけです。

### Go ツールチェイン

```sh
go install github.com/shibukawa/popcornweb/cmd/pw@latest
```

これも動きますし、サポートも続きます。最後に挙げているのは、モジュールが要求する Go
ツールチェインが先に必要になるからです。それこそ、他の 3 つのチャネルが取り除いている
前提条件です。

### 確認

```sh
pw version
```

```
pw 0.1.0 (abc1234, darwin/arm64, go1.27.0)
```

`pw help` はコマンドの一覧を出します。

```
Usage: pw <command> [arguments]

Commands:
  init      create a project in a new directory
  add       enable a capability in a project that declined it
  new       scaffold a handler or a page beside the ones you have
  generate  write everything a compiler needs, stopping before the compiler
  check     report generated files that are stale or missing
  fmt       format template sources into their canonical form
  i18n      reconcile message catalogs against the templates that use them
  migrate   inspect and apply database migrations
  seed      load seed datasets into the database
  build     run generate and then compile the project
  dev       watch, regenerate, rebuild, and restart
  doctor    report what a named environment will actually run
  version   print the version, revision, and toolchain
  help      print this message
```

それぞれのコマンドが何をするか、どこで説明されているかは
[pw コマンド](/ja/pw/overview/)にまとめてあります。

## ライブラリ

Popcorn Web は **Go 1.27 以降**が必要です。

新しいプロジェクトでは、[`pw init`](/ja/pw/project/init/) がフレームワークを require 済みの
`go.mod` を書き出すため、手動の `go get` は不要です。既存モジュールには 1 つだけ手順を
追加します。

```sh
go get github.com/shibukawa/popcornweb
```

アプリケーションコードは、安定したアプリケーション向け API である
[`pw`](/ja/guides/frontend/handlers/) パッケージをインポートします。

```go
import "github.com/shibukawa/popcornweb/pw"
```

## Devbox（任意）

生成されるプロジェクトには、Go と Valkey サービスをピン留めした `devbox.json` が
付属します。`pw init --tailwind` を使った場合は、スタンドアロンの Tailwind CSS
バイナリもピン留めされます。Devbox はツールの再現性を保ちますが必須ではありません。
Go が `PATH` にあれば、`devbox shell` を飛ばして `pw dev` を直接実行できます。

Devbox は [jetify.com/devbox](https://www.jetify.com/devbox/) から導入できます。

## 次のステップ

- [1. はじめる](/ja/tutorial/getting-started/) — 最初のプロジェクトを作って動かす。
