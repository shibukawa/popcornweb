---
title: サーバーレスホスティング
description: Popcorn Web が対応する scale-to-zero / Functions ランタイムと、HTTP アダプターが必要になる条件。
sidebar:
  order: 3
---

「サーバーレス」には互換性のない複数の起動方式があります。区別すべきなのは、ホストが
HTTP プロセスを起動するのか、export されたハンドラーを要求するのか、プロバイダー固有の
イベントを渡すのかです。Popcorn Web は最初の方式と、そこへ HTTP 変換できる方式に、
アプリケーションコードを変えずに対応します。

| ホストの形 | 例 | 対応状況 |
| --- | --- | --- |
| `PORT` が割り当てられる HTTP コンテナ | Cloud Run services、AWS App Runner、Azure Container Apps | 通常の Dockerfile で対応済み |
| invocation を HTTP に変換するアダプター | AWS Lambda Web Adapter | 対応済み。デプロイ物にアダプターを追加 |
| HTTP forwarding custom handler | Azure Functions | HTTP-only function に対応済み |
| リモートビルドされる export 済み Go handler | Vercel Go、Cloud Run functions | source staging 生成で対応済み |
| プロバイダー固有イベント | DigitalOcean Functions、非 HTTP trigger | 非対応 |
| Fetch-event Wasm | Cloudflare Workers | 生成した Worker モジュールで対応済み。TinyGo / ホスト Go |
| Component-model Wasm | Fastly Compute などの WASI HTTP host | 非対応 |

コンテナサービスは別ランタイムではありません。生成済みイメージを起動して `PORT` を
設定するだけで、[`pw.Run`](/ja/reference/runtime/) がそのポートを listen します。
コンテナをゼロまで scale down するサービスも同じです。

build には独立した二つの軸があります。`--target` はデプロイ先、`--backend` は
`nethttp` または `fasthttp` を選択します。`pw dev` の動作は変わりません。

```shell
pw build --target=lambda --backend=nethttp
pw build --target=azure-functions --backend=fasthttp
pw build --target=google-cloud-run-functions --backend=nethttp
pw build --target=vercel-go --backend=fasthttp
pw build --target=cloudflare-workers
```

成果物は `.pw/build/<target>/<backend>/` に生成され、`deployment.json` が付属します。
プロセス型と source 型の target では `config.prod.toml` が必須です。fasthttp build には
`project.fasthttp = true` も必要です。Cloudflare target は `nethttp` だけを build します。

## AWS Lambda

`main` を Lambda イベントハンドラーへ変えるのではなく、
[AWS Lambda Web Adapter](https://github.com/aws/aws-lambda-web-adapter) を使います。
コンテナデプロイなら、runtime stage でアダプターを extensions ディレクトリへ追加します。

```dockerfile
COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:1.0.1 \
  /lambda-adapter /opt/extensions/lambda-adapter
```

アプリケーションの entry point はそのままです。アダプターは `AWS_LWA_PORT`、`PORT`、
`8080` の順で転送先を決め、Popcorn Web の listener も同じ割り当てに従います。生成先には
Linux `bootstrap`、`config.prod.toml`、adapter version を固定した Dockerfile が入り、
Dockerfile が `APP_ENV=prod` を設定します。

フレームワーク自身が Lambda Runtime API client を持たないのは意図的です。Web Adapter は
Function URLs、API Gateway、ALB、buffered response、response streaming を扱いながら、
Lambda 外でも動く一つのイメージを維持できます。

## Azure Functions

バイナリを custom handler として起動し、HTTP request forwarding を有効にします。
ホストが割り当てた listener は `FUNCTIONS_CUSTOMHANDLER_PORT` に入り、`pw.Run` が自動で
認識します。

```json
{
  "version": "2.0",
  "customHandler": {
    "description": { "defaultExecutablePath": "run.sh" },
    "enableProxyingHttpRequest": true
  }
}
```

生成先には Linux handler、`run.sh`、`host.json`、catch-all の `http/function.json` が
入ります。このディレクトリを Functions Core Tools または infrastructure workflow で
upload します。Queue trigger や追加の
input/output binding は通常の HTTP ではなく Azure 独自 payload を使うため、この経路の
対象外です。また Azure Functions は汎用 reverse proxy ではありません。Web アプリ全体なら、
Container Apps または App Service のほうが routing と cold start の制約が少なくなります。

## Vercel Go と Cloud Run functions

Vercel の Go runtime は `api/` 以下に `http.HandlerFunc` を export した `.go` ファイルを
要求します。Cloud Run functions は Go Functions Framework への登録を要求します。どちらも
設定済みの `main` を起動せずソースをリモートビルドするため、ポート名の読み替えだけでは
対応できません。

`pw build` は application module を隔離した source tree へコピーし、選択 backend の `main` を
初期化関数へ変換します。Vercel には `api/Handler`、Cloud Run functions には Functions
Framework の `PopcornWeb` 登録を生成し、warm instance ごとに一度だけ初期化します。

`nethttp` は `pw.Middlewares`、`fasthttp` は `pwfast.Start` と framework の in-memory HTTP/1
bridge を使うため、どちらも provider が要求する `http.HandlerFunc` を公開できます。生成 source は
format、module tidy、vendor 作成、vendor tree からの provider package compile まで成功してから
ready と報告されます。
application checkout ではなく生成ディレクトリを deploy してください。

## Cloudflare Workers

Worker はポートの背後のプロセスではなく、fetch イベントの背後で Wasm モジュールを動かします。
そこでこの target はアプリケーションを Wasm にコンパイルし、それを読み込む JavaScript まで
出力します。アプリケーション側の変更はありません。`pw build` は module を隔離した tree へ
コピーし、source 型 target と同じ方法で `main` を初期化関数へ変換し、middleware chain を
[syumai/workers](https://github.com/syumai/workers) アダプターへ渡すエントリポイントを
追加します。

コンパイラはフラグではなくプロジェクト設定です。デプロイ成果物そのものを決めるからです。
既定は `project.toolchain` で、残りの二つは project name と `pw` のリリースが固定する日付を
既定にします。ホスト Go 向けに scaffold したプロジェクトは標準の `ServeMux` でルーティングし、
そのメソッド付きパターンを TinyGo は照合できないため、そこでの `compiler = "tinygo"` は
拒否されます。TinyGo プロジェクトはどちらのコンパイラも選べます。

```toml
[deploy.cloudflare]
compiler = "tinygo"              # または "go"
name = "myapp"                   # wrangler.jsonc の Worker 名
compatibility_date = "2025-08-01"
```

TinyGo は数 MB のモジュールを作り無料プランに収まります。ホスト Go はその数倍の大きさで
有料プランが必要ですが、build は速く終わります。どちらも同じ適合性検査を通ります。stage には
`build/app.wasm`、フレームワークが所有しコンパイラのバージョンに固定している loader
`build/wasm_exec.js` と `build/worker.mjs`、それらを指す `wrangler.jsonc` が置かれるので、
ディレクトリ全体がそのまま `wrangler dev` と `wrangler deploy` の入力です。

```shell
pw build --target=cloudflare-workers
cd .pw/build/cloudflare-workers/nethttp
npx wrangler dev
```

フレームワークではなくホストに由来する制約が三つあります。Worker にはファイルシステムが
ないので `config.prod.toml` はそこでは読まれません。代わりに build がファイル中のスカラー値を
コンテナと同じ環境変数名で `wrangler.jsonc` の `vars` に展開し、`${NAME}` 参照は同名の
`wrangler secret` から解決されます。`[[middleware.rdb.connections]]` の配列は
`MIDDLEWARE_RDB_CONNECTIONS` という 1 つの環境変数に JSON として載せて持ち込みます（どのホストでも
設定できます）。それ以外の table の配列には環境変数の形がないため、持ち込まれずに名前で報告されます。外部 public ディレクトリは配信できず、
埋め込み tree だけが配信されます。そしてホストはリクエストごとにモジュールを instantiate して
`main` を走らせるため、`observability.boot_log` で形式を指定しない限り起動サマリーはこの
target では出力されません。各リクエストはフレームワークの初期化（数ミリ秒）を払い、memo store
のようなプロセス状態はリクエストをまたいで残りません。ハンドラーはまた、最初の書き込みの前に
リクエストボディを読み切る必要があります。アダプターはその書き込みでレスポンスをホストへ渡し、
以後ホストはボディの読み取りを拒否するからです。

このリクエスト単位の寿命のため、プロセスに状態を持つ設定は「毎リクエスト空になる」のではなく
拒否されます。有効な memo store、`dev-volatile` / `dev-persist` のセッションバックエンド、
memory バックエンドの rate limiter、`d1://` binding 以外の `middleware.rdb` 接続がそれです。
build はコンパイル前に `config.prod.toml` からこれらを報告し、wrangler の var で持ち込まれた
場合は Worker が起動時に再度報告します。いずれも代わりに使うバックエンドを名指しします。

### データベースとしての D1

Worker は [D1](https://developers.cloudflare.com/d1/) に binding 経由で到達します。D1 は SQLite
なので、SQLite で開発しているプロジェクトは 1 行で D1 にデプロイできます。本番の接続でファイルの
代わりに binding 名を書くだけです。

```toml
# config.prod.toml
[[middleware.rdb.connections]]
group = "default"
dsn = "d1://DB"
```

`project.database` は `sqlite` のまま、クエリ・マイグレーション・セッションストア・認証状態は
すでに SQLite 用のものですし、`pw dev` はローカルファイルで動き続けます。build は Wasm に
コンパイルできない SQLite ドライバの代わりに D1 エンジンをリンクし、接続が名指しする binding ごとに
`d1_databases` を書き、各マイグレーションの Up 側を Wrangler 向けに stage します。

```toml
# popcornweb.toml — binding の背後にあるデータベース。id は wrangler deploy に必要で
# wrangler dev には不要
[[deploy.cloudflare.d1]]
binding = "DB"
database_name = "myapp"
database_id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
```

```shell
cd .pw/build/cloudflare-workers/nethttp
npx wrangler d1 migrations apply myapp --local   # または --remote
npx wrangler dev
```

D1 ドライバにはトランザクションがないため、D1 接続では `pw.Transaction` と `auto_transaction` は
失敗します。フレームワーク自身のストアがそうしているように、1 文ずつ書いてください。接続プールも
なく、プール設定は効果を持ちません。

### 外部 public ツリーのための R2

Worker には隣接ディレクトリがないため、[外部 public ツリー](/ja/guides/frontend/public-assets/)は
代わりに R2 バケットから配信します。バケットの binding を名指しすれば残りは build が行います。
生成エントリはバケットからツリーを読み、`wrangler.jsonc` は binding を宣言し、stage にはツリーの
コピーと、各ファイルを URL パス・メディアタイプ付きでアップロードするスクリプトが置かれます。

```toml
# popcornweb.toml
[deploy.cloudflare.r2]
binding = "ASSETS"
bucket_name = "myapp-assets"
```

```shell
cd .pw/build/cloudflare-workers/nethttp
sh r2-upload.sh --local    # wrangler dev のローカルバケットに投入
sh r2-upload.sh --remote   # デプロイ先のバケット
```

マウントはバケットの ETag で応答し、一致すれば `304`、Range リクエストには `206` を返します。
各オブジェクトはリクエストごとに丸ごと読むので、このツリーは「埋め込むには大きすぎるが Worker が
メモリに載せられる」アセット向けです。アプリケーション自身のファイルは
[ストレージインターフェース](/ja/guides/backend/object-storage/)で `backend = "r2"` と binding を
使います。build はそうした binding をすべて `wrangler.jsonc` に宣言し、Worker が届かない `local` と
`s3` バックエンドを拒否します。

### rate limiter のための KV

memory バックエンドの rate limiter は、リクエストごとにプロセスが走るホストでは何も数えられません。
そこで Worker は代わりに [KV namespace](https://developers.cloudflare.com/kv/) で数えます。
この数は推定値です。KV にはアトミックな加算がなく、伝播にも数十秒かかります。rate limit は
それを許容できますがセッションは許容できないので、セッションと認証状態は D1 に置きます。

```toml
# config.prod.toml
[ratelimit]
enabled = true
backend = "cloudflarekv"

[ratelimit.cloudflarekv]
binding = "RATELIMIT"
```

```toml
# popcornweb.toml — binding の背後にある namespace。wrangler dev は placeholder の id で動き、
# wrangler deploy には本物が必要
[[deploy.cloudflare.kv]]
binding = "RATELIMIT"
id = "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
```

1 分より短い window は 1 分間カウントを保持します。KV が受け付ける最短の有効期限だからです。
memo store にはまだ KV バックエンドがなく、Worker は `cache.enabled = false` で build します。

## ランタイム制限は残る

Functions ホストは response を buffer し、実行時間を制限し、idle instance を freeze し、
ローカルには一時ストレージしか提供しないことがあります。Ingress が buffer する場合は
`html.streaming = false`、live response はプロバイダーの上限未満に設定してください。
別 instance に到達し得る request の session と rate limit には共有 backend が必要です。
