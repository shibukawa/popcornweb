# BDF Print Manager向け変更の実装・検証

[依頼原文](bdf-printmanager-public-assets.md)の主要3件と補足3件を実装しました。

## アセットの配置

モジュールルートの `popcornweb.toml` に次の設定を書けます。

```toml
[assets]
public = "cmd/printmanager/public"
derived = "cmd/printmanager/dist/public"
embed = "cmd/printmanager/public.go"
```

`public.go` はアプリと同じ `package main` に置けます。埋め込みパターンは
そのファイルからの相対パスで、上の例なら `//go:embed all:dist/public` です。
生成するGoマニフェストは同じパッケージ、JSONマニフェスト・ルート一覧・中間生成物・
キャッシュは派生ツリーの隣に配置します。コンポーネントから抽出したアセットは
入力ツリーの `generated/` 配下です。

既定値は従来の `public`・`dist/public`・`public.go` です。
未指定と既定値明示でアセット出力がバイト単位で一致することを検証しています。
ルート外への移動、シンボリックリンク、入力と出力が重なる設定は書き込み前に拒否します。
`go:embed` の制約に従い、派生ツリーは埋め込みファイルのディレクトリ配下に置きます。

`pw dev` は `SERVER_PUBLIC_LOCAL_ROOT` で設定した派生パスを子プロセスに渡します。
設定変更も再読込し、入力アセットの編集を監視します。通常のファイル欠落では埋め込みFSへ
フォールバックし、派生ツリーがない場合は起動時に一度警告します。
パスの安全性による拒否はフォールバックしません。

## パッケージングと設定

`config.prod.toml` は存在する場合に同梱し、無い場合は環境変数だけで設定できます。
LambdaのDockerfileは、省略した設定ファイルの `COPY` を生成しません。
ソース型デプロイでも、変換したmainパッケージの相対埋め込みツリーと必要なサブパッケージを保持します。

`[]string` の既定値は `default:"/**"` や `default:"GET,HEAD,POST"` のカンマ区切りです。
空の既定値はタグを省略するか `default:""` とします。`pw generate` は `default:"[]"` などの
配列風の既定値を拒否します。CSRFとCORSの既定値もこの形式に修正しました。

アプリのリソースを開く前に `pw.ParseConfig()`、`pw.RunFrameworkAction()` を呼び、
アクションを処理した場合は戻ります。fasthttpにも同じ公開APIを追加しました。
クエリがなくても `generate.queries = []` が必要なことを含め、日本語・英語のガイドを更新しています。

## 依存更新

ルート・11サンプル・サーバーレスfixture・Wasmの計14個のGoモジュールを更新・整理しました。
既存のGoモジュールパス内で、ルート `go.mod` の宣言済み依存に更新候補が残っていないことを確認しています。

| パッケージ | 更新後 |
| --- | --- |
| tinybind-go | v0.5.34 |
| tinygodriver | v1.3.5 |
| localotelviewer | v1.0.4 |
| esbuild | v0.28.2 |
| compress | v1.20.3 |
| goose | v3.29.0 |
| go-redis/v9 | v9.23.0 |
| syumai/workers | v0.36.0 |
| Astro | 7.4.1 |
| Starlight | 0.42.6 |
| vscode-languageclient | 10.1.2 |
| @vscode/vsce / ovsx | 4.0.0 / 1.3.0 |

サイトと拡張は `npm outdated` で更新候補がない状態です。
拡張の対応下限はVS Code 1.91、開発・パッケージング用Node.jsは22です。
同梱フォーマッターをTinyGo 0.42.0で再ビルドし、WASIの `poll_oneoff` に対応しました。
依存更新で変わったフォームとキャッシュキーの生成コードも再生成しています。

新しいフォームバインダーに合わせ、`pw.Parse` の後や事前にフォームを解析した場合でも
`pw.FormValue` が値を保持するよう互換処理を追加しました。本文の再利用領域には上限があります。

## 検証結果

- `go test -p=4 ./...` と `go vet ./...` 成功。
- `go test -p=2 -tags=pwdev ./middlewares ./pwfast` 成功。
- 11サンプルすべてでコード生成・依存整理・`go test ./...` 成功。
- ルートが `package fixture`、アプリが `cmd/app` のfixtureで、生成・開発配信・編集反映・本番配信を検証。
- 同fixtureで、設定ファイルなしのLambda ELF生成とVercelソースパッケージのコンパイル、設定ファイルがある場合の同梱を検証。
- 同じ配置の最小アプリで、設定ファイルなしのCloudflare Workers向けWasmビルド成功。
- 同じアプリでAzure FunctionsとGoogle Cloud Run functionsもビルド成功。Lambda・Azure・Google・Vercel・Cloudflareの全5ターゲットを確認。
- `--generate-config` と `healthcheck` がアプリのリソースを開かないことを検証。
- 拡張の122テスト、全74入力でのWasm再ビルドとの比較、VSIXのローカルパッケージング成功。
- サイト205ページのビルド成功。

BDFの実環境やクラウドへのデプロイは実行していません。依頼の再現条件は、このリポジトリ内のfixtureで検証しています。
