---
title: カスタムコマンド
description: Web サーバーと同じクエリやアプリケーションコードを再利用し、バッチ処理や運用タスクを追加する。
sidebar:
  order: 3
---

`pw build` が生成するバイナリには、Web サーバーだけでなく独自のサブコマンドも持たせられます。
データのインポート、バックフィル、定期バッチ、運用時のメンテナンスなどが主な用途です。
Web ハンドラと同じアプリケーション内で動くため、生成済みのクエリ関数やドメインコードを
そのまま再利用でき、別の CLI プロジェクトを用意する必要はありません。

開発ツール自体については [pw コマンド](/ja/pw/overview/)を参照してください。
設定ファイル、環境変数、設定スキャフォールド、値の優先順位については
[アプリケーション設定](/ja/guides/architecture/configuration/)にまとめています。

## 独自のサブコマンド

サブコマンドに必要なのは、構造体 1 つと登録呼び出し 1 つだけです。`pw generate` が
呼び出し箇所を読んでパーサーを書き出すため、別にフラグの配線を保守する必要はありません。

```go
package main

type importCommand struct {
	Path   string   `arg:"required" help:"CSV file to import"`
	Label  string   `arg:"optional"`
	Tags   []string `arg:"*"`
	DryRun bool     `default:"false" help:"parse without writing"`
}
```

| タグ | 意味 |
| --- | --- |
| `arg:"required"` | 必須の位置引数 |
| `arg:"optional"` | 省略可能な位置引数 |
| `arg:"*"` | 可変長の位置引数（0 個以上） |
| *(`arg` タグなし)* | オプション。設定フィールドと同じタグが使える |

設定がパースされる前に登録し、そのあとディスパッチします。

```go
func main() {
	handlers.RegisterConfig()
	pw.RegisterSubCommand[importCommand]("import", "import a CSV file")

	if err := pw.ParseConfig(); err != nil {
		log.Fatal(err)
	}
	if command, ok := pw.Command[importCommand](); ok {
		if err := runImport(context.Background(), command); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := pw.Run(context.Background(), handlers.Handlers()); err != nil {
		log.Fatal(err)
	}
}
```

```sh
./myapp import ./data/users.csv --dry-run
```

`pw.Command[T]` はそのサブコマンドが選択されたときにだけ `true` を返すので、`if` 文が
そのままディスパッチになります。入力を受け取るのは選ばれたサブコマンドだけです。
`pw.ParseConfig` を自分で呼んでも安全です。`pw.Run` も呼びますが、パースは 1 回しか
行われません。

登録の順序が重要なのは設定と同じ理由です。生成された定義はパッケージの `init` で登録
されるため、`RegisterSubCommand` はすべての `init` の後、`ParseConfig` の前に実行する
必要があります。パース後の登録は panic します。

ひとつだけ取られている名前があります。`healthcheck` はフレームワークの
[コンテナヘルスプローブ](/ja/guides/deployment/operational-endpoints/#シェルの無いコンテナからのプローブ)の
ものであり、登録すると起動時に panic します。すでに
`HEALTHCHECK CMD ["/myapp", "healthcheck"]` と書かれた Dockerfile はプローブを意味し
続けなければならないので、衝突はどちらかが覆い隠されるのではなく、すぐに失敗します。

サブコマンドはサーバーのパース済み設定を共有します。そのため `pw.Config[T]` は、
DSN を含めてサーバーと同じ値を返し、同期すべき 2 つ目の設定経路を作りません。

データベースプールは別です。これは `ParseConfig` ではなく `pw.Run` や `pw.Middlewares`
が開きます。接続が必要なサブコマンドは、設定された DSN から自分で開いてください。
