---
title: 概要
description: pw 開発ツールの役割と、各コマンドの位置づけ。
---

1 つのコマンドがプロジェクトのライフサイクルをつなぎます。`pw` は最初のファイルを
スキャフォールドし、テンプレートと SQL を Go へコンパイルし、開発ループを動かし、
データベースを管理して、リリースビルドを作ります。

```sh
pw help
```

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
  request   send one request to the running application, routed by its OpenAPI
  version   print the version, revision, and toolchain
  help      print this message
```

インストールは Homebrew、Nix、リリースアーカイブ、Go ツールチェインのいずれかで行えます。
[インストール](/ja/start/installation/)を参照してください。

## コマンド一覧

### プロジェクト

| コマンド | 用途 |
| --- | --- |
| [`pw init`](/ja/pw/project/init/) | 動くプロジェクトを作る |
| [`pw add`](/ja/pw/project/add/) | init で断った機能をあとから追加する |
| [`pw new`](/ja/pw/project/new/) | ハンドラ・ルート・テンプレートをまとめて追加する |
| [`pw generate`](/ja/pw/project/generate/) | ビルドに必要な入力を一式書き出し、コンパイラの手前で止まる |
| [`pw check`](/ja/pw/project/check/) | 生成された Go が古いか欠けているかを報告する |
| `pw fmt` | テンプレートとクエリのソースを正規形に整形する |
| `pw i18n` | メッセージカタログを、それを使うテンプレートと突き合わせる |
| [`pw dev`](/ja/pw/project/dev/) | 監視、再生成、マイグレーション、再起動 |
| [`pw build`](/ja/pw/project/build/) | リリース用バイナリを作る |
| [`pw doctor`](/ja/pw/project/doctor/) | その環境で何が動き、どこが間違っているかを報告する |
| [`pw request`](/ja/pw/project/request/) | 起動中のアプリケーションへリクエストを 1 つ送る。OpenAPI 文書で振り分ける |
| `pw rename` | テンプレートの宣言と、それを名指しているものすべてを改名する |
| `pw lsp` | Language Server Protocol でエディタ向けの解析を提供する |

`pw fmt`、`pw i18n`、`pw rename`、`pw lsp` には専用のページがなく、それぞれが属する作業の
ページで扱っています。整形は [`pw check`](/ja/pw/project/check/#ci-では) にあります。ビルドの中で
整形をどこで走らせるか、なぜ生成より前なのかを説明している場所だからです。カタログの
コマンドは[ページの翻訳](/ja/guides/frontend/i18n/)にあり、カタログが支えているメッセージ
構文のすぐ隣に置いてあります。`pw rename` と `pw lsp` は[エディタ対応](/ja/productivity/editor-support/)にあります。
前者は普段エディタから触るもので、後者は自分では起動しないものだからです。

### データベース

| コマンド | 用途 |
| --- | --- |
| [`pw migrate`](/ja/pw/database/migrate/) | マイグレーションの確認、適用、ロールバック |
| [`pw seed`](/ja/pw/database/seed/) | シードデータセットの読み込み |

## プロジェクトの探索

`pw init` 以外のコマンドにはプロジェクトが必要ですが、プロジェクトルートで実行する
必要はありません。`pw` は作業ディレクトリから上へたどって `popcornweb.toml` を
探すため、どのサブディレクトリからでも動きます。最上層まで見つからなければ
`popcornweb.toml not found` で失敗します。

## 終了ステータス

成功で `0`、コマンドの失敗で `1`、コマンドが与えられなかった場合は `2` です。エラーは
`pw:` を前置して標準エラー出力に書かれます。
[`pw request`](/ja/pw/project/request/) だけは curl のコードを共有し、`2` が使い方
エラー、`-f` 下の失敗ステータスが `22` です。

## 混同しないように

デプロイするバイナリには別のコマンドラインがあります。設定オプション、設定
スキャフォールドの出力、アプリケーション定義のサブコマンドです。この境界は
[カスタムコマンド](/ja/guides/architecture/custom-commands/)で扱います。
