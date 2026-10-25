---
title: WebSocket
description: 双方向プロトコルを入力用と出力用の構造体で宣言し、送受信データのエンコード処理を生成します。
sidebar:
  order: 3
---

[ストリーム](/ja/guides/frontend/streams/)は一方通行です。ハンドラが送り、クライアントは
読むだけで、クライアントが何かを言ったのは接続を開いたリクエストの一度きり。進捗の通知、
モデルが返すトークン、ログの追尾——ソケットが要るように見えるものの大半は、実のところ
これで足ります。HTTP のフレーミングをそのまま保つので、アップグレードを知らない
WebSocket のアップグレードに対応していないプロキシ経由でも利用でき、失敗は通常の problem レスポンスで返せます。まず
ストリームを検討してください。

WebSocket が要るのは、クライアントが**そのあとも喋り続ける**ときです。チャット、共同編集、
ブラウザが購読してから購読内容を変えていく制御チャネル。それだけはストリームにはできません。

`pw.WebSocket[In, Out]` はリクエストをアップグレードし、接続をコールバックに渡します。

```go
package handlers

import (
	"net/http"

	"github.com/shibukawa/popcornweb/pw"
)

// プロトコルを 2 つの構造体として書く。どちらの向きも、種類は判別用フィールドが持つ。
// ストリームのイベントの書き方と同じです。
type ClientMsg struct {
	Type string `json:"type"` // "join" | "say"
	Name string `json:"name"`
	Text string `json:"text"`
}

type ServerMsg struct {
	Type string `json:"type"` // "welcome" | "message" | "error"
	From string `json:"from"`
	Text string `json:"text"`
	Code string `json:"code"`
}

func Chat(w http.ResponseWriter, r *http.Request) {
	// リクエストから要るものは、この時点で読んでおきます。
	room, _ := pw.QueryValue(r, "room")

	if err := pw.WebSocket(w, r, func(s *pw.Socket[ClientMsg, ServerMsg]) error {
		for {
			in, err := s.Read()
			if err != nil {
				return nil // 相手が去ったか、無言のままタイムアウトした
			}
			switch in.Type {
			case "say":
				err = s.Write(ServerMsg{Type: "message", From: room, Text: in.Text})
			default:
				err = s.Write(ServerMsg{Type: "error", Code: "unknown_type"})
			}
			if err != nil {
				return err
			}
		}
	}); err != nil {
		// 拒否のレスポンスはもう送られています。これはログのため。
		pw.Logger(r).Warn("upgrade refused", pw.Err(err))
	}
}
```

呼び出しには型引数がひとつも書かれていません。生成がコールバックの引数から `ClientMsg` と
`ServerMsg` を読み取り、前者にはデコーダを、後者にはエンコーダを `_pw_gen.go` に書きます。
このファイルはビルド生成物で、人が編集するものではありません。`Read` は `ClientMsg` を返し、
`Write` は `ServerMsg` を取ります。`[]byte` も `encoding/json` も、ハンドラのどこにも出てきません。

`omitempty` を付けないメッセージ型は全フィールドを書き出すので、クライアントは判別用
フィールドを読み、その種類が使わないフィールドを無視します。空になるフィールドに
タグを付ければ送られなくなります。エンコーダの規則は
[レスポンス](/ja/guides/frontend/responses/#json) と同じものです。とはいえ、この手の
プロトコルではタグを付けないほうが既定として優れています。フィールドが常に在ると
当てにできるクライアントは欠落時の分岐を持たずに済みますし、判別付き共用体が空文字列
3 つを省いて浮かせるバイト数は、節約する価値のあるバイト数ではありません。

この生成は省けません。型が見つかっていないソケットはコンパイルも接続も通り、最初の
メッセージで初めて落ちます。つないだ直後に切れるソケットに出会ったら、ほかを見る前に
`pw generate` を走らせてください。

## 誰がどれを呼べるか

`Read` を呼べるのは 1 つの goroutine だけです。`Write` はどこからでも呼べます。ランタイム自身の
制御フレームと同じロックを取るので、100 個のソケットに配信する goroutine がメッセージの
途中にフレームを割り込ませることはありません。生の接続を自分で持つ場合との差はここです。
そちらでは同時書き込みが通信路を壊し、しかもサーバ側には何の診断も出ません。

送るだけのハンドラでも、誰かが読み続けていなければなりません。ping と close のフレームは
読み取りの中で処理されるからです。タイマーで書くだけで読まないコールバックは、ping に
答えず、close にも気づかず、最初のアイドルタイムアウトで理由を告げずに死にます。送信専用の
ハンドラも読み取りループは回し、受け取ったものを捨てます。

閉じ方はコールバックから戻ることです。どう戻ろうとランタイムが close フレームを送って
接続を畳むので、書き忘れた `defer` のせいで相手が close を待ち続けることはありません。

## コールバックが触れてはいけないもの

コールバックの中で `w` と `r` を読んではいけません。fasthttp ビルドではコールバックが走るのは
ハンドラが**戻ったあと**で、そこにあるリクエスト値は次にそのスロットを使うリクエストのもの
だからです。コールバック内の `r.Header.Get` は、他人のヘッダを読みます。必要なものは先に
取り出しておく——例の `pw.QueryValue` がそれです。

`r.Context()` と、そこから辿るもの——`pw.RequestAuthentication`、自分で入れたコンテキスト値——は
先に取り出して持ち込めます。`r.RemoteAddr` は駄目です。fasthttp への書き換え表にこの綴りが
無いので、これを読むハンドラは第 2 のビルドに拒否されます。

## 誰が接続してよいか

アップグレードのリクエストはセッションクッキーを持った `GET` で、CSRF ミドルウェアには
届きません。あちらが見張っているのは安全でないメソッドだからです。だから他サイトからの
アップグレードを受け入れることは、接続が開いたままになる CSRF だと言えます。ハンドシェイクの
前にフレームワークがオリジンを確かめるのはこのためで、比較は CSRF が使うものと同じです。
リクエストの `Origin` は、このデプロイ自身のオリジンか、`security.csrf.trusted_origins` に
書かれたものでなければなりません。

デプロイ前に知っておく価値のある帰結が 2 つあります。

**TLS を終端するプロキシの後ろでは、そのプロキシを宣言してください。** 比較にはスキームが
含まれますが、アプリケーションの中で TLS を終端するものは何もありません。プロキシを宣言して
いないデプロイは自分のオリジンを `http://…` と解決し、ブラウザは `https://…` を名乗るので、
すべてのアップグレードが拒否されます。CSRF チェックが元々必要としているのと同じ宣言です。

```toml
[server]
trusted_proxies = ["10.0.0.0/8"]
```

**`Origin` ヘッダの無いリクエストは通します。** これを送る義務があるのはブラウザだけなので、
無いということはサービスかコマンドラインのクライアントです。ここで拒否すると、ブラウザ以外の
呼び出し元をすべて締め出すことになります。その接続を守るのは認証で、どのみち必要なものです。
文字列としての `null`——サンドボックス化されたフレームが送るもの——はブラウザなので、拒否します。

エンドポイント自身のポリシーが本当に要るときは、`pw.WebSocketWith` が呼び出しごとに受け取ります。
そちらがフレームワークの判断より優先されます。

```go
opts := pw.SocketOptions{
	CheckOrigin: func(origin, host string) bool { return origin == "https://partner.example" },
}
_ = pw.WebSocketWith(w, r, opts, chatLoop) // 上と同じコールバックに名前を付けたもの
```

## 上限とデッドライン

どの接続も、読み取り上限、アイドルデッドライン、ping の間隔、書き込みデッドラインを持ちます。
どれも無効にはできません。上限の無い読み取りは、何をしても回収できない goroutine と接続に
なるからです。既定値は、`gorilla/websocket` のアプリケーションが元々動いていた値に揃えてあります。

| 項目 | 既定値 | 何を縛るか |
| --- | --- | --- |
| `ReadLimit` | 1 MiB | 受信メッセージ 1 通。伸長後の大きさで数え、超えると 1009 で接続を閉じる |
| `IdleTimeout` | 60s | 読み取りデッドライン。pong のたびに延びる |
| `PingInterval` | 54s | ランタイムが ping を送る間隔。`IdleTimeout` より短いこと |
| `WriteTimeout` | 10s | 書き込み 1 回。応答しない相手が書き手を占有しないように |

プロセス全体は起動時に、個別のエンドポイントは `pw.WebSocketWith` で変えられます。

```go
pw.SetSocketDefaults(pw.SocketOptions{
	IdleTimeout:  90 * time.Second,
	PingInterval: 30 * time.Second,
})
```

`PingInterval` が `IdleTimeout` 以上の場合はハンドシェイクの時点で拒否されます。それは接続が
死んだと判定されたあとにしか発火しないタイマーだからです。

## 失敗はどこへ行くか

`pw.WebSocket` が返すのはハンドシェイクのエラーだけです。`nil` でなければアップグレードは
拒否され、[problem レスポンス](/ja/guides/frontend/responses/#エラー)はすでに書かれています。
ハンドラはそれを記録するか数えるかであって、自分で応答してはいけません。

そこから先の失敗には、載せられるステータスがもう残っていません。コールバックが返した
エラーは `pw.SetStreamErrorHandler` で設置したハンドラに届きます。ストリームの途中で起きた
失敗と同じ受け口で、名前が stream なのは 1 つの設置で両方を覆うからです。設置しておかないと、
本番で落ちたソケットは何も言いません。

```go
pw.SetStreamErrorHandler(func(err error) {
	slog.Error("socket", "error", err)
})
```

## 2 つのビルドで同じソース

`fasthttp = true` のプロジェクトはこのハンドラを 2 回ビルドしますが、コールバックの中身は
どちらも同じ文字列のままです。`pw.Socket` と `pwfast.Socket` は同じ型で、書き換えが動かすのは
import の修飾子だけ。それを買っているのが上の寿命の規則です。規則が書かれているのは
コールバックがハンドラより長生きする側のためで、net/http ビルドには何のコストもありません。

TinyGo では、`pw.Run` がハンドラに接続を渡せるリスナ経由で待ち受けます。TinyGo 自身の
`net/http` はアップグレードを完了できず、エラーもログも出さないままハンドシェイクを
ハングさせるからです。これはフレームワークの仕事で、書き足す行はありません。

## リファレンス

`Socket.Subprotocol`、`Socket.Close`、オプションの全フィールドといった一覧は
[ランタイムリファレンス](/ja/reference/runtime/#レスポンスを書く)にあります。一方通行の
レスポンスとどちらを選ぶかは、[ストリーム](/ja/guides/frontend/streams/)がもう半分です。
