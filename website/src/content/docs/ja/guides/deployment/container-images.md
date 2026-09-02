---
title: コンテナイメージ
description: Popcorn Web のイメージが COPY と go build では作れない理由と、生成された Dockerfile の各行が何のためにあるのか。
sidebar:
  order: 2
---

Go のプロジェクトなら、まずこう書くはずです。

```dockerfile
FROM golang:1.27 AS build
COPY . .
RUN CGO_ENABLED=0 go build -o /out/myapp ./cmd/myapp
```

Popcorn Web のプロジェクトでは、これは失敗します。しかも失敗の仕方が親切では
ありません。テンプレートが宣言したレンダラ、`.pw.sql` が名付けたクエリ関数、
ページツリーを繋ぐ登録 — コンパイラは未定義シンボルとしてそれらを並べますが、
どれもリポジトリに無いファイルのものです。

それらはビルド生成物です。`pw generate` がソースの隣に `_pw_gen.go` として書き、
`pw init` がそのパターンを `.gitignore` に入れます。Tailwind が出力する CSS も、
`public.go` が埋め込む `dist/` 以下のアセットツリーも同じです。Popcorn Web の
ビルドにはコンパイラの前に**ホストフェーズ**があり、いきなり `go build` に進む
Dockerfile はそこを飛ばしています。

`pw init` が書く Dockerfile は飛ばしません。このページはその中身の解説です。
実行するだけでなく、書き換えられるように。

## 生成される Dockerfile

```dockerfile
FROM golang:1.27-trixie AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

RUN GOBIN=/usr/local/bin go install \
      github.com/shibukawa/popcornweb/cmd/pw@$(go list -m -f '{{.Version}}' github.com/shibukawa/popcornweb)

COPY . .

RUN CGO_ENABLED=0 pw build

FROM gcr.io/distroless/static-debian13:nonroot
WORKDIR /app

COPY --from=build /src/myapp /app/myapp
COPY --from=build /src/config.prod.toml /app/config.prod.toml

ENV APP_ENV=prod
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD ["/app/myapp", "healthcheck"]

ENTRYPOINT ["/app/myapp"]
```

ビルドと起動はこうです。

```bash
docker build -t myapp .
```

実際に生成されるファイルには、ここに挙げた判断それぞれにコメントが付いています。
以下はその理由のほうです。

### `go build` ではなく `pw build`

[`pw build`](/ja/pw/project/build/) はホストフェーズとコンパイラをまとめた一つの
コマンドです。生成し、Tailwind が有効なら CSS をコンパイルし、派生アセットを
組み立て、エントリポイントが開発専用パッケージに到達していればビルドを拒否し、
最後にリンクします。これをビルダーステージの中で走らせるからこそ、ホストの
ツールチェインも持ち込んだ生成物も無しに、クリーンなチェックアウトから同じ
イメージが作れます。

`CGO_ENABLED=0` は依然として重要です。後段のランタイムベースが要求する静的さを
生むのがこの指定だからです。

### pw のバージョンはフレームワーク自身が決める

pw が生成するコードは、特定バージョンのフレームワークが読みます。両者は一致して
いなければなりません。Dockerfile にバージョンを書いてずれていくのを待つかわりに、
モジュールグラフから読み出します。

```dockerfile
RUN GOBIN=/usr/local/bin go install \
      github.com/shibukawa/popcornweb/cmd/pw@$(go list -m -f '{{.Version}}' github.com/shibukawa/popcornweb)
```

`go.mod` のフレームワークを上げれば、ビルダーもついてきます。更新すべき二箇所目は
ありません。このファイルの中で、自分自身を保守する唯一のバージョン固定です。

### distroless、そして scratch ではない理由

`gcr.io/distroless/static-debian13:nonroot` には CA 証明書、タイムゾーンデータ、
非特権ユーザーが入っていて、シェルが入っていません。scratch のほうが小さいですが、
多くのアプリケーションでは誤りです。最初の外向き HTTPS — OIDC のディスカバリ
ドキュメント、トークン交換、任意の API 呼び出し — が証明書プールを見つけられずに
TLS ハンドシェイクで落ち、しかもエラーは足りないファイルではなく相手先を名指し
します。

`:nonroot` タグは uid 65532 で動きます。リスナが特権ポートではなく 8080 なので
これで足ります。書き込み可能なファイルシステムも不要です。アセットはディスク
からではなく埋め込みツリーから配信されます。

両方のステージが Debian のリリース名を明示しています。ビルダーは
`golang:1.27-trixie`、ランタイムは `static-debian13` です。素の `golang:1.27` は
新しい Debian stable が出たその日にベースが載せ替わります。ビルド環境が自分では
なく Debian のスケジュールで変わるということで、distroless 側が追いつくまで
二つのステージが別のリリースに乗ることにもなります。Debian を上げるときは、
二行を一緒に変えてください。

### `WORKDIR` と `APP_ENV` は飾りではない

プロジェクトローカルな設定は**プロセスの作業ディレクトリ**を基準に解決されます。
つまり `WORKDIR /app` と、その隣に置かれた設定ファイルは、二つの判断ではなく
一つです。ファイルを動かさずにバイナリだけ動かすと、サーバーは失敗するかわりに
デフォルト値で起動します。二つのうち静かなほうの間違いで、気づきにくいほうです。

`ENV APP_ENV=prod` も同じです。`APP_ENV` が未設定だと `dev` に解決されるので、
この行が無いイメージは `config.dev.toml` を探し、見つけられず、デフォルトで
立ち上がります。解決順の全体は[アプリケーション設定](/ja/guides/architecture/configuration/)に
あります。

### プローブはバイナリ自身

イメージには curl も、それを動かすシェルもありません。`HEALTHCHECK` は
アプリケーションを呼びます。

```dockerfile
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s \
  CMD ["/app/myapp", "healthcheck"]
```

このサブコマンドはイメージが既に持っている設定をそのまま読みます。ポートもパスも
ここで繰り返さないのはそのためです。必要なのは `server.health` が設定されている
ことで、未設定ならキー名を挙げて `1` で終了します。`pw init` が
`config.prod.toml` にこのキーを書くのは、まさにこのためです。プローブそのもの、
`--ready`、Compose と Kubernetes が同じ問いをどう投げるかは
[ヘルスチェックと readiness](/ja/guides/deployment/operational-endpoints/) に
あります。

## `.dockerignore` が守っているもの

```
**/*_pw_gen.go
dist/
config.dev.toml
*.db
.devbox/
```

面白いのは最初の二つです。イメージはどちらも作り直すので、ビルドコンテキストに
持ち込まれたホスト側のコピーは、よくて上書きされ、悪ければリンクされます。消した
ソースから生成されたファイルはそれでもコンパイルが通り、その登録処理も動いて
しまうからです。

`dist/` を除くと `dist/public/.keep` も消えます。空のツリーでも `go:embed` が
成功するための番人ですが、ここでは安全です。アセットビルドがコンパイラの読む前に
ディレクトリを作り直します。

`config.dev.toml` を除くのは、そこにローカルの DSN と、この一台のために生成された
keyring シークレットが入っているからです。イメージを pull できる誰もが読める
レイヤーに、どちらも置くものではありません。

## 設定とシークレット

`pw init` は `config.dev.toml` の隣に `config.prod.toml` を書き、Dockerfile が
それをコピーします。ポート、health と readiness のパス、JSON のログ出力が入って
いて、シークレットは一切入っていません。

これは好みではなく制約です。イメージレイヤーは pull できる誰もが読めますし、
後のレイヤーでファイルを消しても前のレイヤーからは消えません。デプロイ側が持つ値は
かわりに二つの経路で届きます。ファイル内の `${NAME}` 参照は読み込み時に展開され、
データベース接続はこの形で書かれています。

```toml
[[middleware.rdb.connections]]
group = "default"
dsn = "${DATABASE_URL}"
```

未定義の名前は空の DSN ではなく読み込みエラーになります。変数を渡し忘れれば、
どこにも繋がらないプールができるかわりにサーバーが止まります。他のシークレットには
それぞれ専用の環境変数があります。`SESSION_KEYRING_SECRET`、
`SESSION_COOKIE_SECRET`、`SESSION_REDIS_DSN` です。

設定の解決は最初に読めた候補を採ってそこで止まります。ファイルをマージしません。
つまり `config.prod.toml` は差分ではなく完全な設定であり、セッションやログインを
持つプロジェクトについては、スキャフォールドが最後まで書ききることはできません。
それらのセクションは開発用のエンドポイントと、この環境のために生成された
シークレットで埋まっていて、そのまま持ってくるわけにいかないからです。生成された
ファイルは書けなかったセクションの名前を残します。完全な一式はアプリケーション
自身が出力します。

```bash
APP_ENV=prod ./myapp --generate-config=toml
```

## マイグレーションは別の手順

このイメージはマイグレーションを適用しません。起動時の自動適用が開発以外で無効なのは
意図的です。複数インスタンスが同時に起動すれば競合しますし、ローリングデプロイの
最中に前進のみの適用が走るのは、誰も承認していないスキーマ変更です。
[`pw migrate up`](/ja/pw/database/migrate/) は独立した手順として実行します。
Kubernetes の Job、リリースフェーズ、新しいリビジョンがトラフィックを受ける前に
走らせるタスク。pw と `migrations/` ディレクトリを持つイメージなりランナーなりから
実行してください。

## TinyGo でビルドする

TinyGo のプロジェクトには `Dockerfile.tinygo` が付きます。二つの差はビルダー
ステージだけです。

```dockerfile
FROM tinygo/tinygo:0.42.0 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

RUN GOBIN=/usr/local/bin go install \
      github.com/shibukawa/popcornweb/cmd/pw@$(go list -m -f '{{.Version}}' github.com/shibukawa/popcornweb)

COPY . .

RUN pw generate
RUN tinygo build -scheduler=threads -o /out/myapp ./cmd/myapp
```

```bash
docker build -f Dockerfile.tinygo -t myapp .
```

[`pw generate`](/ja/pw/project/generate/) は `pw build` からコンパイラを引いた
ものです。生成、スタイルシート、アセット、開発用インポートの検査まで同じ手順を踏み、
リンクの手前で止まります。その次の一行が二つのファイルの差のすべてで、コンパイラを
フラグの裏に隠さず書き下しているのもそのためです。出力パス、ターゲット、最適化
レベル — 変えたくなるのはこの行です。

`-scheduler=threads` はネットワークプロトコルを話すエンジンには必須です。協調型
スケジューラの下ではブロッキングなソケット呼び出しがランタイム全体を掴むため、
ドライバのキャンセル監視が動かず、クエリはコンテキストのデッドラインを越えて
`nil` エラーを返し、ログには何も残りません。ソケットを開かないエンジンには無害
なので、スキャフォールドは無条件に渡します。PostgreSQL や MySQL を使いながら
これを外すと、静かなほうの版が出荷されるかわりに、識別子を名指ししてビルドが
失敗します。

見返りははるかに小さいバイナリです。それと引き換えにイメージの振る舞いが二つ
変わり、どちらもアプリケーション側では直せません。

**SIGTERM が効きません。** TinyGo の `os/signal` はデフォルトの動作を置き換えた
うえで、チャネルには何も届けません。そのためフレームワークはこのツールチェインでは
ハンドラを登録しません。`docker stop` も Pod の削除も `SIGTERM` を送り、猶予期間を
待ち、毎回 `SIGKILL` に至ります。`shutdown_timeout` は一度も動かず、処理中の
リクエストはドレインされずに切られます。停止シグナルより前にトラフィックを止めて
ください — Kubernetes の `preStop` の sleep か、先にドレインするロードバランサ。
そして猶予期間は短くします。返ってこない応答を待っても、kill が遅れるだけです。

**マイグレーションをプロセス内で実行できません。** TinyGo ではマイグレーション
ランナーが `pw` を子プロセスとして呼びます。背後のエンジンがホスト専用でリンク
できないからです。distroless のイメージに pw は入っていないので、プロセス内の
適用はバイナリが無いというエラーになります。もっとも実際には何も変わりません。
マイグレーションは前述のとおり既に別手順のものです。

TinyGo は `GOOS` と `GOARCH` を読まず、ビルドを走らせているマシン向けにコンパイル
します。ターゲットと同じアーキテクチャでビルドするか、
`docker buildx --platform` を使ってください。

:::note[Dockerfile を使わないイメージ]
[ko](https://ko.build/) と
[Cloud Native Buildpacks](https://buildpacks.io/) はどちらも Dockerfile 無しで
Go のイメージを作ります。ただし置き換えているのは Dockerfile であって、ホスト
フェーズではありません。`go build` の手順を握るのはこれらの側で、生成は
走らせてくれません。先に作業ツリーで `pw generate` を実行し、そのあとでビルダーを
呼びます。どちらも git のインデックスではなく作業ディレクトリを読むので、
`.gitignore` が除外している生成物がそこにあれば、そのまま使われます。チェック
アウト直後にビルダーを呼ぶ CI ジョブは、このページ冒頭の未定義シンボルに行き
着きます。TinyGo はどちらも非対応で、Docker の `HEALTHCHECK` も生成されません。
設定した health パスへのプラットフォーム側のプローブがその代わりです。
:::

## このファイルに手を入れないほうがよいとき

生成された Dockerfile が推奨の道で、たいていのプロジェクトで変える必要があるのは、
組織で標準にしているベースイメージくらいです。別のものを選ぶのは、プラットフォームが
既に他のサービス全部を自前のやり方でビルドしている場合です。十数個のサービスを
Buildpacks で回しているチームなら、このファイルより一貫性のほうが得るものが大きい
でしょう。ホスト Go のプロジェクトで、ホストフェーズが先に走るなら。

変わらないのはそのホストフェーズだけです。何がイメージを作るにせよ、コンパイラの
前に `pw generate` か `pw build` が走ります。走らなければ、コンパイラには読むものが
ありません。

ここで触れた設定キーの全体は[設定リファレンス](/ja/reference/configuration/)に
あります。
