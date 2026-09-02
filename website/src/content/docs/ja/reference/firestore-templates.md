---
title: Firestore クエリフォーマット
description: 型付きの Go API に変換される firestore 構造体タグと .pw.firestore クエリの全仕様。
sidebar:
  order: 5
---

Firestore の宣言元は 2 つです。`firestore` タグを付けた Go 構造体がエンティティ、キー、
kind を定義し、`.pw.firestore` ファイルがそのエンティティへの名前付きクエリを定義します。
`pw generate` は両方を、ソースの隣の `_pw_gen.go` ビルド出力へコンパイルします。

クライアントが使うのは Firestore の **Datastore mode** API です。設定とデプロイについては
[Firestore](/ja/guides/storage/firestore/)を参照してください。

## 生成対象のディレクトリ

どちらのソースも `generate.firestore` に列挙したディレクトリへ置きます。

```toml
[generate]
firestore = ["entities"]
```

対象外にある `.pw.firestore` は stray source として報告されます。型ごとにエンティティの
コーデック、キービルダー、kind、ポリシー情報が生成され、export した statement ごとに
公開 Go 関数が 1 つ生成されます。

## `firestore` タグ

```text
firestore:"<プロパティ名>[,<オプション>...]"
```

プロパティ名を空にすると Go のフィールド名を使います。`firestore:"-"` は、identity 系の
オプションが付いていない限りフィールドを除外します。非公開フィールドは常に除外されます。

| オプション | 意味 |
| --- | --- |
| `name` | string フィールドをキーの name にする |
| `id` | `int64` フィールドをキーの数値 ID にする |
| `parent` | `datastore.Key` フィールドを ancestor path にする |
| `version` | read が返したエンティティバージョンを `int64` で受け取る |
| `ttl` | TTL ポリシーが使う保存済み `time.Time` プロパティを示す |
| `noindex` | プロパティを保存するがインデックスには入れない |
| `omitempty` | ゼロ値ならプロパティ自体を書かない |

1 つの型に置ける `name` または `id`、`parent`、`version`、`ttl` は、それぞれ最大 1 つです。
`noindex` のプロパティは、絞り込み、並べ替え、`select`、`distinct` に使えません。
`ttl` が公開するのはプロパティ名だけで、Google Cloud の TTL ポリシーは別途適用します。

`name`、`id`、`parent` のフィールドは、通常のプロパティマップには入りません。同じ値を
プロパティにも保存したい場合に限り、明示的な名前を付けます。

```go
ID string `firestore:"external_id,name"`
```

## プロパティの型

| Go の型 | Datastore の値 |
| --- | --- |
| `string` とその名前付き型 | string |
| `int`〜`int64`, `uint8`, `uint16`, `uint32` | integer |
| `float32`, `float64` | double |
| `bool` | boolean |
| `[]byte` | blob |
| `time.Time` | timestamp。マイクロ秒精度で保存 |
| `datastore.Key` | key |
| `datastore.LatLng` | geographic point |
| `[]T` | array |
| 同じパッケージの構造体 | embedded entity |
| `*T` | 参照先。nil は null |
| `datastore.Value` | 指定された値をそのまま保存 |

`uint`、`uint64`、`uintptr`、map、関数、channel は生成エラーです。`datastore` タグだけを
持ち `firestore` タグを持たないフィールドも拒否されます。2 つのマッパーが異なる
プロパティ名を割り当てると、生成時の型検査が意味を失うためです。

## クエリ文法

```text
[export] statement <Name>(<param>: <GoType>, ...): firestore.<shape><<Entity>> {
  where <condition>
  ancestor {param}
  select <property>, ...
  distinct <property>, ...
  order <property> [asc|desc], ...
  start {param}
  end {param}
  limit <n>|{param}
  offset <n>|{param}
  index <property> [asc|desc], ...
}
```

句はすべて省略可能で、順序も自由です。1 行に複数書く場合は `;` で区切り、`//` 以降は
コメントです。`export` の有無は Go の名前が公開かどうかと一致させます。kind 句は
ありません。結果型が kind を決めます。

### 戻り値の形

| 形 | 生成される戻り値 | リクエスト数 |
| --- | --- | --- |
| `firestore.batch<T>` | `(firestorebind.Page[T], error)` | 1 回 |
| `firestore.many<T>` | `iter.Seq2[T, error]` | イテレーションが進むごとに 1 ページ |
| `firestore.count<T>` | `(int64, error)` | 集計クエリ 1 回 |
| `firestore.keys<T>` | `(firestorebind.KeyPage, error)` | keys-only クエリ 1 回 |

生成される関数は、先頭に `context.Context`、続いて宣言したパラメータ、最後に任意の
`datastore.ReadOption` を取ります。`batch`、`count`、`keys` には
`*firestorebind.Tx` を取る `NameTx` 版も生成されます。`many` にはありません。
トランザクション内でリクエスト回数が見えないイテレータを使わせないためです。

### 条件

比較演算子は `==`、`!=`、`<`、`<=`、`>`、`>=`、`in`、`not in` です。`and`、`or`、
括弧で組み合わせます。`and` は `or` より強く結合します。`in` と `not in` には、保存する
プロパティと要素型が一致する slice パラメータが必要です。

```text
where sensor == {sensor} and at >= {from}
where sensor in {sensors}
where (sensor == {sensor} or site == {site}) and at > {from}
```

すべてのプロパティ名とパラメータ型がエンティティのタグに対して検査されます。キーだけの
フィールドは `where` に書けません。ancestor path には `ancestor` を使います。

### ページング、projection、インデックス

`start` と `end` は `datastore.Cursor` パラメータを取ります。大きな `offset` は、読み飛ばす
エンティティにも読み取りと課金が発生するため、再開には cursor を使ってください。

`select` で選ばなかったフィールドはゼロ値になります。projection の結果を `Store` や
`Update` に渡してはいけません。Datastore には部分更新がなく、省略したプロパティまで
ゼロ値で置き換わります。`distinct` のプロパティは `order` の先頭と一致させます。

単一プロパティのインデックスは自動です。複合インデックスは `index` 句で宣言し、生成された
定義をデプロイします。生成処理はインデックスの要否を推測しません。必要なインデックスが
なければ、サービスが実行時に `FAILED_PRECONDITION` を返します。

## 直接操作する API

生成されたエンティティは `firestorebind` の各操作に渡せます。

```go
h, err := firestore.Handle(ctx)
key, err := h.Store(ctx, value)
key, err = h.Insert(ctx, value)
value, err = h.Load[Entity](ctx, key)
err = h.Update(ctx, value)
err = h.Remove(ctx, value)
```

`Store` は upsert、`Insert` は未作成のエンティティ、`Update` は作成済みのエンティティに
使います。不完全な数値キーを `Insert` に渡すと、サーバーが割り当てたキーが返ります。
`firestore.Handle` は `database/firestore` が持つプロセスハンドルのアクセサです。
トランザクションはハンドルの `Run` と `*firestorebind.Tx` の各操作を使います。

ドライバのエラーは失われません。構造化された Datastore エラーは
`firestorebind.AsError`、not-found や precondition の判定は `errors.Is` を使います。
