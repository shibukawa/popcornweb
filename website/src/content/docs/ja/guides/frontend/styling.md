---
title: スタイリング
description: コンポーネントスコープのスタイルと、Tailwind CSS の有効化。あとから有効にする手順も。
sidebar:
  order: 5
---

Popcorn Web は CSS の手法を 1 つに強制しません。コンポーネントスコープのスタイルは
設定なしで使え、Tailwind CSS はオプトインのビルドツールです。両方を同じ
コンポーネントで使っても、クラス名を奪い合いません。

## コンポーネントスタイル

コンポーネントは自分の head の内容を提供でき、そのクラス名は自動的にスコープされます。

```html
export component Card(label: string): html {
<head>
<style>
.box { color: red }
</style>
</head>
<div class="box"><span>{label}</span></div>
}
```

ブロック内で宣言されたクラスはリネームされ、対応する `class` 属性も書き換えられます。
そこで宣言されて**いない**クラスは手を加えられずに通過するので、同じ属性の中で Tailwind
のユーティリティとスコープ付きルールを並べられます。
[テンプレート](/ja/guides/frontend/templates/)を参照。

## Tailwind CSS

Popcorn Web は**スタンドアロン**の Tailwind バイナリを実行します。`package.json` も
`node_modules` も Node のロックファイルもありません。

最短の方法は、プロジェクトの作成時に有効にすることです。

```sh
pw init myapp --tailwind
```

### あとから有効にする

Tailwind を後から追加するには、スキャフォールドが作るものと同じ 4 つを揃えます。
既存プロジェクトには独自のファイルがあるかもしれないため、明示的に追加してください。

**1. `tailwindcss` バイナリを使えるようにする**

`pw dev` と `pw build` は `PATH` 上の `tailwindcss` を探し、見つからなければ明確な
メッセージで失敗します。`devbox.json` に追加します。

```json
{
  "$schema": "https://raw.githubusercontent.com/jetify-com/devbox/0.14.2/.schema/devbox.schema.json",
  "packages": ["go@latest", "git@latest", "valkey@latest", "tailwindcss_4@4.2.4"],
  "shell": {"init_hook": ["echo 'Popcorn Web development environment'"]}
}
```

新しいパッケージを `PATH` に載せるためシェルに入り直します。

```sh
devbox shell
```

バージョンを固定すると CSS ビルドの再現性を保てます。固定しなければ、同じソースでも
ツールの更新によって出力が変わりえます。別のツール管理方法で `tailwindcss` を `PATH`
に置くなら、Devbox は不要です。

**2. スタイルシートのエントリポイントを作る**

```css
/* assets/app.css */
@import "tailwindcss";
@source "../handlers";
@source "../templates";
```

import は Tailwind を開始しますが、テンプレートの場所までは伝えません。`@source` の
行が `.pw.html` 内のクラス名を Tailwind に見せます。これがないと生成されるスタイル
シートはほぼ空です。テンプレートを含むディレクトリごとに 1 つ追加してください。
[プロジェクト構成](/ja/guides/architecture/project-structure/)のレイアウトなら
`@source "../webroot";` になります。

`@import "tailwindcss"` の行はビルド前に検証されるので、壊れたエントリポイントは
黙って空の CSS を出力するのではなくエラーとして報告されます。

**3. `popcornweb.toml` で有効にする**

```toml
[assets.tailwind]
enabled = true
input = "assets/app.css"
output = "public/generated/app.css"
minify = true
```

`input` と `output` はプロジェクトルートからの相対パスで、互いに異なる必要があります。
`enabled` が true でパスを省略した場合、この 2 つが既定値になります。

**4. ドキュメントシェルから出力をリンクする**

```html
package templates

external AssetURL(name: string): url

export component Document(children: html?): html {
<!doctype html>
<html lang="en"><head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>My App</title>
  <link rel="stylesheet" href={AssetURL("generated/app.css")}>
</head>
<body><slot /></body></html>
}
```

引数は `public/` 内での `output` のパスです。`server.public.mount`（既定 `/public`）と、
デプロイ後にこのスタイルシートを毎回再検証させずキャッシュさせるリビジョンセグメントは
`AssetURL` が付けます。このシェルと宣言の裏の Go は `pw init --tailwind` が書き出します。
ディレクトリの埋め込みと配信、そしてリビジョンが何を買うのかは
[静的ファイル配信](/ja/guides/frontend/static-assets/)を参照してください。

あとは開発サーバを起動します。

```sh
pw dev
```

### 実行のされ方

| コマンド | 動作 |
| --- | --- |
| `pw dev` | 非 minify で 1 回ビルドしてからウォッチャを起動。ウォッチャが落ちたら入力を再監視 |
| `pw build` | ファイルの `minify` 設定に関わらず minify して 1 回ビルド |

出力はまず一時ファイルに書かれ、その後で所定の場所へリネームされます。そのため、
サーバーが書きかけのスタイルシートを見ることはありません。
`public/generated/app.css` はビルド生成物です。スキャフォールドされた `.gitignore`
は圧縮サイドカー `public/**/*.br`, `public/**/*.zstd`, `public/**/*.gz` をすでに除外しており、生成 CSS も通常は同じく
無視対象にします。

### プラグイン

エントリポイントから参照されるローカルプラグインは、エントリポイントからの相対で解決
され、ビルド前に存在が確認されます。

```css
@import "tailwindcss";
@plugin "./plugins/typography.mjs";
@source "../handlers";
```

モジュールはプロジェクト内に置いてください。`assets/plugins/*.mjs` がスキャフォールド
の慣習です。これによりパッケージマネージャなしでビルドの再現性が保たれます。
