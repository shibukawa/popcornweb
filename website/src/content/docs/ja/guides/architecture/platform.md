---
title: プラットフォーム
description: Popcorn Web アプリケーションが今日動く場所の一覧。それぞれの成果物を作るビルドオプションと、各ホストが取り上げるもの。
sidebar:
  order: 8
---

`net/http` に向けて一度書いたアプリケーションが、ネイティブバイナリ、コンテナイメージ、4 つの
プロバイダー向け Functions バンドル、Cloudflare Worker の Wasm モジュールとして出荷できます。
ソースはどれでも同じです。変わるのは `pw build` のフラグ 1 つか `popcornweb.toml` のテーブル 1 つ、
そしてもう少し見えにくいところで、ホストがアプリケーションに残してくれるもの、つまりファイル、
ソケット、リクエストより長く生きるプロセスです。このページは全部を横に並べます。選ぶ根拠を
デプロイコマンドではなく、その制約に置くためです。コマンドのほうは簡単な部分です。

## 一覧

| プラットフォーム | 作り方 | コンパイラ | 備考 |
| --- | --- | --- | --- |
| ネイティブバイナリ（Go が対象とする OS） | `pw build` | ホスト Go | プロジェクトルートにバイナリ。`GOOS` / `GOARCH` は `go build` にそのまま渡る |
| ネイティブバイナリ（小型） | `pw generate` のあと `tinygo build -scheduler=threads ./cmd/myapp` | TinyGo 0.42 | `project.toolchain = "tinygo"`。strip 済みホストビルドの半分未満 |
| コンテナ: Cloud Run services、App Runner、Container Apps、Kubernetes | scaffold 済みの `Dockerfile` か `Dockerfile.tinygo` で `docker build` | どちらでも | ホストが `PORT` を設定。ランタイムは distroless |
| AWS Lambda | `pw build --target lambda` | ホスト Go | Linux バイナリと、Lambda Web Adapter を固定した Dockerfile |
| Azure Functions | `pw build --target azure-functions` | ホスト Go | Linux バイナリと `host.json`。HTTP を転送する custom handler |
| Google Cloud Run functions | `pw build --target google-cloud-run-functions` | ホスト Go（リモート） | Functions Framework の登録を持つ vendored ソース |
| Vercel Go | `pw build --target vercel-go` | ホスト Go（リモート） | `api/` にハンドラー 1 つを持つ vendored ソース |
| Cloudflare Workers | `pw build --target cloudflare-workers` | `[deploy.cloudflare]` に従い TinyGo かホスト Go | Wasm モジュール、フレームワーク所有の loader、`wrangler.jsonc` |

行を横断する軸が 2 本あります。`--backend fasthttp` は最後の行以外のすべてで同じソースを fasthttp
向けにコンパイルし、生成側が後半を書くために `project.fasthttp = true` を要求します。Worker target は
`nethttp` だけです。`--target` の成果物は `.pw/build/<target>/<backend>/` に、何を作ったかを記す
`deployment.json` と並んで置かれ、プロセス型と source 型の target は `config.prod.toml` の存在を
要求します。`pw dev` はここにあるどのオプションにも影響されません。プロジェクトが何として出荷される
かにかかわらず、ループバックでホストプロセスを動かします。

## どれを取るか

既定は最初の行をコンテナで動かす形で、表の見た目より圧倒的に多くの場面でそれが正解です。割り当て
`PORT` の背後で動くホスト Go バイナリは、ファイルの SQLite、プロセス内の memo store、graceful shutdown、
プロセス内のマイグレーションといったフレームワークの機能をすべて保ちますし、scaffold 済みの
`Dockerfile` がすでにそれをビルドします。以下はどれもその上位版ではありません。ポートの背後のプロセスが
提供されない場所で動かす手段であり、それぞれ代価を払います。

TinyGo を取るのは成果物のサイズが制約のときです。メガバイト単位で測られるイメージ、アップロード上限の
あるエッジランタイム、デバイス。数字は[パフォーマンスガイド](/ja/guides/architecture/performance/)に
あります。代価は、アプリケーションコードでは直せない 2 つの挙動です。`SIGTERM` は届かないので停止は
猶予期間後の kill になり、マイグレーションはプロセス内ではなく `pw` を呼び出します。加えて、ビルドは
それが走るマシン向けにコンパイルします。TinyGo でネットワークプロトコルを話すエンジンには
`-scheduler=threads` が必要で、忘れると静かな不具合ではなくコンパイルエラーになります。

Functions ホストを取るのは、組織がすでにそれを運用していて、アプリケーションがその呼び出しモデルに
収まる Web サービスのときです。Lambda と Azure Functions は変更なしのバイナリを起動して HTTP を転送し、
Cloud Run functions と Vercel は stage したソースを自分でビルドします。各契約は
[サーバーレスガイド](/ja/guides/deployment/serverless/)が順に説明しています。コールドスタート、
場合によってはバッファされるレスポンス、そして停止ではなくリクエスト間で凍結されるインスタンスを
見込んでください。

Cloudflare Workers を取るのは、アプリケーションをエッジで動かしたく、リクエストごとに起動する
プログラムで構わないときです。TinyGo ならモジュールは無料プランのアップロード上限に収まり、ホスト Go は
数秒でビルドできますが有料プランが要ります。制約は一覧の中で最も鋭く、次の節で述べます。

## 各ホストが取り上げるもの

フレームワークのストアはそれぞれホストから何かを必要とします。下の表が、ある設定がデプロイ先で正しいか
どうかを決めるものです。「拒否」と書いた行は起動時に、Worker target ではビルド時にも検査されるので、
食い違いは「何も動かないストア」ではなく、代替を名指しするエラーになります。

| 機能 | プロセスとコンテナ | Functions ホスト | Cloudflare Workers |
| --- | --- | --- | --- |
| SQL データベース | sqlite ファイル、PostgreSQL、MySQL | PostgreSQL、MySQL。sqlite ファイルはインスタンスのディスクと同じ寿命 | `d1://BINDING` の D1（SQLite 方言）。それ以外は拒否 |
| セッションバックエンド | rdb、cookie、redis、dynamo、firestore | 同じ | cookie、または D1 上の rdb。開発用バックエンドは拒否 |
| rate limiter | 1 レプリカなら memory、共有は redis | redis | `cloudflarekv`。memory は拒否 |
| memo store | 可 | 可（インスタンスごと） | Cache API バックエンドができるまで拒否 |
| オブジェクトストレージ | local ディレクトリ、s3 | s3 | r2 binding。local と s3 は拒否 |
| 外部 public ツリー | バイナリ隣接のディレクトリ | stage にコピー | `[deploy.cloudflare.r2]` で名指しした R2 バケット |
| graceful shutdown | 可。TinyGo では不可 | プロバイダーが管理 | なし。プログラムはリクエストと共に終わる |
| マイグレーション | プロセス内、または `pw migrate` | データベースに対して `pw migrate` | stage したファイルに `wrangler d1 migrations apply` |

Worker の列は 1 つの事実から導かれます。ホストはリクエストごとにモジュールを instantiate して `main` を
走らせるので、プロセスに置いたものは何も残らず、Worker はファイルシステムもソケットも持ちません。
だからデータベースは binding、ファイルはバケット、カウンタは namespace で、起動サマリーは既定で
出ません。ハンドラーが最初の書き込みの前にリクエストボディを読み切らなければならないのも同じ理由です。

## ここにないプラットフォーム

WASI HTTP ホスト（Fastly Compute やコンポーネントモデルのランタイム）向けの Wasm モジュールは
`tinygo build -target=wasip1` でビルドでき、アプリケーションはそこから動きますが、その ABI 向けの HTTP
アダプターはまだ出荷していません。トリガーが HTTP ではないプロバイダーのイベント関数、DigitalOcean
Functions や上記各プロバイダーの非 HTTP トリガーも一覧の外です。`main` をプロバイダー固有のハンドラーに
書き換えて到達するプラットフォームも同じです。上の各行はアプリケーションの `main` を保つか機械的に
変換するかのどちらかで、著者にプロバイダーの型を採用させる target は追加しません。

フラグの全部は [`pw build`](/ja/pw/project/build/) のリファレンス、各ツールチェーンが設定するタグは
[ビルドタグ](/ja/reference/build-tags/)、2 つの Dockerfile の全文は
[コンテナガイド](/ja/guides/deployment/container-images/)にあります。
