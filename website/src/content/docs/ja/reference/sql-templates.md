---
title: SQL クエリフォーマット
description: .pw.sql の全仕様。ステートメントの種類、パラメータ型、条件付き SQL、predicate と relation、生成時の検査について説明します。
sidebar:
  order: 3
---

`.pw.sql` は `pw generate` が Go にコンパイルする型付きクエリ言語です。中に書いた SQL は
SQL のままで、翻訳も書き換えも移植もされません。一方で Go との境界は検査されます。
パラメータの型、結果のカラム、`WHERE` 句の有無は、どれもビルド時に決まります。

ここでは `.pw.sql` の言語仕様をまとめます。生成された API と生の接続の使い分けや、
ステートメントが接続を解決する仕組みについては[クエリー](/ja/guides/storage/queries/)と
[リレーショナルデータベース](/ja/guides/storage/rdb/)にあります。

## ファイルの構成

```sql
package queries

type User {
  id: int
  name: string
  active: bool
}

export statement GetUser(id: int): sql.one<User> {
SELECT id, name, active
FROM users
WHERE id = {id}
}
```

ファイルは、生成コードが属する Go パッケージから始まります。`.pw.sql` は1ファイルごとに
自分の `_pw_gen.go` にコンパイルされ、同じディレクトリのファイルはすべて同じ Go パッケージに
入ります。生成が読むのは `popcornweb.toml` の `generate.queries` が挙げるディレクトリだけで、
どれにも属さない `.pw.sql` は黙って飛ばされるのではなく報告されます。

[コンポーネントパッケージ](/ja/guides/deployment/package/)では `generate.queries` が空で
なければなりません。生成されたクエリは1つのエンジンのプレースホルダ構文を持ちますが、
パッケージは利用側のエンジンを知りようがないからです。

| 宣言 | 導入するもの |
| --- | --- |
| `package name` | 生成ファイルが属する Go パッケージ |
| `type Name { field: T … }` | 結果の形。同名の Go 構造体になる |
| `statement name(…): kind { … }` | パッケージ内だけのステートメント |
| `export statement Name(…): kind { … }` | 同じものを Go の API として公開する |

`type` が見えるのは、それを宣言したファイルのステートメントだけです。それでも名前は
ディレクトリ全体で一意でなければなりません。どのファイルの構造体も同じ Go パッケージに
入るからです。この2つがぶつかるのは、2つのファイルが同じ形を返すときです。別のファイルが
宣言した型を名指しすると `unknown type Name` で失敗し、同じ名前でもう一度宣言すると
`duplicate generated template declaration Name` で失敗します。結果の型を共有する
ステートメントは1つのファイルにまとめてください。どうしても分けるなら、ファイルごとに
別の名前で同じ形を宣言します。片方が `NewProjectID`、もう片方が `NewTicketID` という具合です。

## ダイアレクト

プレースホルダのトークンは `popcornweb.toml` の `project.database` から来ます。`postgres`
なら `$1`、`$2`、…、`mysql` と `sqlite` なら `?` です。どちらでも書くのは `{name}` で、
生成されるシグネチャも同一なので、エンジンを変えると出力される SQL のテキストだけが変わり、
呼ぶ側は何も変わりません。

ダイアレクトが変えるのはそのトークン**だけ**です。それ以外は書いたまま生成された SQL に
届きます。`||` は `CONCAT` に書き換えられず、`ON CONFLICT` は `ON DUPLICATE KEY UPDATE` に
翻訳されず、MySQL に `RETURNING` が無いことも回避されません。その種の翻訳層は正しく見えて
微妙に壊れます——`||` は PostgreSQL と SQLite では連結ですが MySQL では論理和なので、
書き換えると述語が反転しうるのです。選んだエンジンに向けて書いてください。

したがって生成されたパッケージ1つが対応するエンジンは1つです。これは、PostgreSQL の本番に
対してテストで SQLite を使おうとする前に量る価値があります。両者は `RETURNING` と
`ON CONFLICT` を共有するので素朴な CRUD はしばしば通ります。ただ、通ったことを確かめる
ものは何も無く、動かしているパッケージは出荷するパッケージではありません。

## 型

| テンプレートの型 | Go の型 |
| --- | --- |
| `string`, `decimal` | `string` |
| `bool` | `bool` |
| `int` | `int` |
| `float` | `float64` |
| `bytes` | `[]byte` |
| `datetime`, `date`, `time` | `time.Time` |
| `url` | `url.URL` |
| `T[]` | `[]T` |
| `T?` | `*T` |

この表は Go の型で止まります。ドライバも同意する必要があります。NULL がありうるところでは
オプショナル型を使ってください。必須の `string` が NULL を読むと、空文字列ではなくエラーに
なります。

2つの行には、ドライバの同意だけでは足りないものがあります。

- `url` のカラムは双方向ともテキストとして運ばれます。`url.URL` のパラメータは文字列形式で
  バインドされ、返ってきたカラムは解析し直されます。`database/sql` は構造体をバインドする
  ことも scan することもできないからです。
- `datetime`、`date`、`time` はドライバが `time.Time` を返すことを要求します。MySQL なら
  DSN の `parseTime=true`、SQLite ならドライバとカラムの宣言型次第です。SQLite 自身は
  日付型を持ちません。いずれにせよドライバの設定であって、ダイアレクトの選択では決まりません。

## ステートメントの種類

| 種類 | 契約 | 生成される結果 |
| --- | --- | --- |
| `sql.exec` | 行の結果を持たない | `sql.Result` |
| `sql.one<T>` | ちょうど1行 | `T`。0行は `sql.ErrNoRows`、複数行はエラー |
| `sql.optional<T>` | 0行または1行 | `*T`。0行は `nil, nil`、複数行はエラー |
| `sql.many<T>` | 0行以上 | `iter.Seq2[T, error]`。ためずに流す |
| `sql.predicate` | 再利用できる条件 | 無し——他のステートメントからだけ使える |
| `sql.relation<T>` | 型付きのサブクエリ | 無し——他のステートメントからだけ使える |

`sql.many` は1行ずつ scan して渡します。イテレータの裏にスライスはたまりません。range を
break すると背後の `sql.Rows` が閉じ、クエリ・scan・反復のエラーはすべてエラー値として
届きます。

```go
for user, err := range queries.ListActiveUsers(ctx, true) {
	if err != nil {
		return err
	}
	consume(user)
}
```

## パラメータ

本文の `{name}` はすべて、宣言されたパラメータを運ぶプリペアドステートメントのプレース
ホルダです。テンプレートの式が SQL のテキストへ連結されることはないので、値のバインドから
インジェクションの余地は生まれません。

```sql
export statement RenameUser(id: int, name: string): sql.exec {
UPDATE users
SET name = {name}
WHERE id = {id}
}
```

```go
statement, err := queries.BuildRenameUser(42, "Ada")
// statement.SQL  == "... SET name = $1 WHERE id = $2 ..."
// statement.Args == []any{"Ada", 42}
```

この保証は絶対で、その分の代償があります。手書きの `$1` や `?` は生成エラーですし、値の
パラメータは構造的な要素——テーブル名、カラム名、演算子、ソート方向——の代わりには決して
なれません。

パラメータ名として拒否されるのは 2 つ、`ctx` と `db` です。生成される全関数の公開シグネチャ
でコンテキストと実行体が使う名前だからです。それ以外は `err` も `result` も使えます。生成
コードが自分で導入する変数にはアンダースコアを前置しているためです。

### スライスの展開

スライスのパラメータは値のリストへ展開されます。

```sql
export statement FindUsers(ids: int[]): sql.many<User> {
SELECT id, name, active
FROM users
WHERE id IN ({ids})
ORDER BY id
}
```

空のスライスには妥当な描き方が無いので、ビルダは `IN ()` を出す代わりにエラーを返します。
空の場合を呼び出し側で扱うか、条件で別の SQL 構造を選んでください。

## 結果の型と SELECT のカラム

結果のフィールドの順序は SELECT や RETURNING のカラム順と一致し、各カラム名か別名は
フィールド名に対応している必要があります。生成が両方を検査するので、結果の型から離れた
SELECT リストはクエリではなくビルドを失敗させます。

```sql
type UserSummary {
  id: int
  displayName: string
}

export statement ListUsers(): sql.many<UserSummary> {
SELECT id, display_name AS displayName
FROM users
ORDER BY id
}
```

この検査が成り立つのは形が静的に分かる場合だけです。だから実行時の条件で SELECT や
RETURNING のカラムを増減させることはできません。どの分岐でも結果の形は同じにしてください。

## 条件付き SQL

```sql
export statement SearchUsers(name: string, activeOnly: bool): sql.many<User> {
SELECT id, name, active
FROM users
WHERE name = {name}
{if activeOnly}
  AND active = TRUE
{/if}
ORDER BY id
}
```

`{else}` も使えます。条件は `bool` である必要があります。プレースホルダを消費するのは
残った分岐だけなので、どの分岐が生き残っても番号と `Args` は揃ったままです。

### 条件の間の演算子とコンマ

`AND` も `OR` も、コンマも括弧も、書き手が管理する必要はありません。条件がすべて成立した
ときに欲しいステートメントを書き、そこから条件を抜き出す形にします。

```sql
export statement SearchUsers(
  name: string, city: string, minAge: int,
  hasName: bool, hasCity: bool, hasAge: bool, staffOnly: bool
): sql.many<User> {
SELECT id, name, city, age
FROM users
WHERE
  {if hasName}name LIKE {name}{/if}
  AND {if hasCity}city = {city}{/if}
  AND ({if hasAge}age >= {minAge}{/if} OR {if staffOnly}role = 'staff'{/if})
ORDER BY id
}
```

`{if}` の囲みを消しながら読めば、それがレンダされる SQL です。`hasCity` だけを立てると
`WHERE city = $1` になります。余る演算子は出力されず、空になった括弧のグループは自分の括弧と
それを繋いでいた `AND` を連れて消え、`city` は `$2` ではなく `$1` になります。どの条件も
立てなければ `WHERE` 自体が現れません。余らない演算子は書いた位置にそのまま出ます。改行と
インデントも保たれるので、いま動いている述語は同じバイト列をレンダします。

演算子は 2 つの条件の間、つまり外側のテキストに置いてください。完成したステートメントで
演算子が座る位置がそこであり、だからこそソースが SQL として読めます。分岐の内側に置いた
`{if hasCity}AND city = {city}{/if}` も同じように動き、古いテンプレートはそう書かれて
いますが、2 つを繋ぐものがその 1 つの条件の一部として読めてしまいます。

対象は `WHERE`、`HAVING`、`QUALIFY`、JOIN の `ON`、およびその内側の括弧グループです。
コンマは `SET`、`VALUES`、`INSERT` のカラムリスト、`ORDER BY`、`GROUP BY`、`FROM`、
`WITH`、`WINDOW`、`USING`、`PARTITION BY` で管理されます。`ORDER BY` や `GROUP BY` は
全項目が条件付きなら、`WHERE` と同じように自分のキーワードを落とします。

```sql
export statement AddUser(id: int, name: string, city: string, withCity: bool): sql.exec {
INSERT INTO users (id, name{if withCity}, city{/if})
VALUES ({id}, {name}{if withCity}, {city}{/if})
}
```

カラムとその値は**同じ条件**で囲んでください。生成は各分岐の経路を追って両者の個数が
一致して終わることを要求するので、独立した 2 つの条件がそれぞれ対応する組を囲むのは通り、
1 つの `{if}/{else}` がカラムを選び同じ `{if}/{else}` がその値を選ぶのも通ります。複数行の
`VALUES`、`INSERT … SELECT`、カラムリストのない `INSERT`、リスト内の `sql.predicate` は、
推測せず未決のまま残されています。

`SELECT` と `RETURNING` のコンマは書いたままです。条件付きの結果カラムがそもそも禁止
されていて、その拒否が先に答えを出すからです。SELECT リストの `OVER (…)` も同じ理由で
結果コンテキストなので、条件付きの `PARTITION BY` 項目は `WINDOW` 句に置いてください。

### 意図的に手を出さない範囲

単語が直前にある括弧はグループではなくデータなので、`IN ({ids})` のリストや関数の引数
リストは、どの分岐でも括弧とコンマを保ちます。引数を 1 つ落とせば呼び出しの引数個数が
変わってしまうからです。`USING (…)` も同様で、同じ括弧が
`DELETE FROM t USING (SELECT …) s` では派生テーブルを運びます。

`BETWEEN` を閉じる `AND` は句ではなくその形式に属するので、条件をまたいで分割すると生成
エラーになります。`BETWEEN` 全体を条件の内側に入れてください。

```sql
-- 拒否される
WHERE n BETWEEN {lo} {if hasHi}AND {hi}{/if}
```

`CASE` のアームは句でもリストでもないので、一緒に出力を控えられるキーワードもセパレータも
ありません。空の断片は `CASE WHEN THEN` を残してしまいます。だから `CASE` の内側で何も
出力しえない断片は生成エラーです。

```sql
-- 拒否される
WHERE CASE WHEN {if flagA}a{/if} THEN 1 ELSE 0 END = 1
```

その条件に出力する `{else}` を与えれば、空になりえなくなるので通ります。分岐ごとに括弧の
ネストが揃わない場合も、対応を推測せずエラーになります。

以上はどれも後述の更新規則を緩めません。`UPDATE` と `DELETE` の `WHERE` は依然として
全分岐で非空であることの証明が必要で、`SET` の項目がすべて条件付きの `UPDATE` も拒否
されます。出力を控えられたコンマは何も埋めないからです。

## predicate と relation

private な `sql.predicate` は再利用できる条件です。

```sql
statement minimumID(id: int): sql.predicate {
id >= {id}
}

export statement FindRecentUsers(minimum: int): sql.many<User> {
SELECT id, name, active
FROM users
WHERE {minimumID(minimum)}
ORDER BY id
}
```

private な `sql.relation<T>` は `FROM subquery` や `JOIN subquery` で使える型付きの
サブクエリです。名前は大文字で始めます。`FROM subquery` が relation として読むのは
その綴りだけなので、`activeUsers` と宣言すると使う側で
`subquery relation name must be PascalCase` になります。

```sql
statement ActiveUsers(minimumID: int): sql.relation<ActiveUser> {
SELECT id, name
FROM users
WHERE id >= {minimumID} AND active = TRUE
}

export statement ListActiveUsers(minimumID: int, name: string): sql.many<ActiveUser> {
SELECT active_users.id, active_users.name
FROM subquery ActiveUsers(minimumID) AS active_users
WHERE active_users.name = {name}
ORDER BY active_users.id
}
```

組み立ててもパラメータのリストは分断されません。サブクエリと外側の引数は1つのプレース
ホルダ列を共有し、最終的な SQL に現れる順に並びます。別名は明示で、小文字のスネークケース
です。再帰する relation は拒否されます。

どちらも export できず、どちらも自分の関数を生成しません。

## 2つの安全規則

### UPDATE と DELETE には WHERE 句が要る

句が空になりうるかどうかは実行時のデータではなくテンプレートの性質なので、証明は丸ごと
生成時に走り、生成コードにガードは入りません。

```sql
-- 拒否される: ある呼び出し経路が全行を消す
export statement UnsafeDelete(id: int, enabled: bool): sql.exec {
DELETE FROM users
{if enabled}WHERE id = {id}{/if}
}

-- 受理される: 句が空になる経路が無い
export statement SafeDelete(id: int, name: string, byID: bool): sql.exec {
DELETE FROM users WHERE {if byID}id = {id}{else}name = {name}{/if}
}
```

キーワードはステートメント自身のものである必要があります。サブクエリ、CTE の本体、文字列
リテラル、コメントの中の `WHERE` は要件を満たしません。同じ証明が動的な `SET` リストも
覆い——代入がすべて条件付きの UPDATE はエラーです——どの結果種別にも適用されるので、
`sql.one<T>` と宣言された `DELETE … RETURNING` も同じように証明されます。`sql.predicate`
が要件を満たすのは、それ自身がどの経路でも空にならないときだけです。

意図した全行 UPDATE・DELETE のための抜け道はありません。それは
[マイグレーション](/ja/productivity/migrations/)として書いてください。

### SELECT のカラムは結果の型と一致する

上で述べたとおりで、これは同じ考えのもう半分です。結果カラムを条件で変えられない規則と
合わせて、生成された構造体を、そのステートメントが返しうるすべての行の正確な記述に保ちます。

## `export` と名前の大小

`export` が決めるのは、そのステートメントがパッケージの公開 Go API に加わるかどうかです。
生成される関数の名前は宣言そのままなので、Go が読むのは名前自身の大小であり、それは
`export` と一致していなければなりません。

| 宣言 | 生成 | |
| --- | --- | --- |
| `export statement FindUser(…)` | `func FindUser(…)` | 公開 API |
| `statement findUser(…)` | `func findUser(…)` | パッケージ内。どこからでも呼べる |
| `export statement findUser(…)` | — | エラー: `export` は非公開の名前を公開できない |
| `statement FindUser(…)` | — | エラー: `export` 無しでその名前は公開になってしまう |

`sql.predicate` と `sql.relation` は例外です。実行されるのではなく他のステートメントの
ビルダに埋め込まれるので、自分の名前の関数を生成せず、`export` と揃えるものがありません。
predicate はどちらの綴りでも通ります。relation は `export` なしのパスカルケースで書きます。
[`FROM subquery`](#predicate-と-relation) がそれ以外の綴りを受け付けないからです。

## 生成されるシグネチャ

Popcorn Web が生成するのは、宣言された名前での**コンテキスト解決**形です。公開された関数は
`*sql.DB` も `*sql.Tx` も取りません。実行子はコンテキストから来ます。だから同じ関数が
トランザクションの中でも外でも動きます。

```go
func Name(ctx context.Context, p ...P) (sql.Result, error)   // sql.exec
func Name(ctx context.Context, p ...P) (T, error)            // sql.one<T>
func Name(ctx context.Context, p ...P) (*T, error)           // sql.optional<T>
func Name(ctx context.Context, p ...P) iter.Seq2[T, error]   // sql.many<T>

func BuildName(p ...P) (sqlbind.Statement, error)            // export された全ステートメント
```

`p ...P` は対応づけられたテンプレートのパラメータです。private なステートメントは同じ組を
`name` と `buildName` で受け取ります。

`Statement` は生成パッケージごとではなく
`github.com/shibukawa/tinybind-go/sqlbind` に1回だけ宣言されているので、値はそのまま
パッケージ境界を越えます。

```go
type Statement struct {
	SQL  string
	Args []any
}
```

`BuildName` を使うのは、SQL のテスト、ログ行、独自のデータベース抽象です。

```go
statement, err := queries.BuildGetUser(42)
log.Printf("sql=%s args=%v", statement.SQL, statement.Args)
```

## どこで走るか

`.pw.sql` の中にデータベースを名指すものは何もありません。コンテキストが、通常のリクエスト
では有効な接続グループのプールを、`pw.Transaction` の中では進行中のトランザクションを運びます。

```go
err := pw.Transaction(r, func(ctx context.Context) error {
	if _, err := queries.InsertUser(ctx, name); err != nil {
		return err
	}
	return queries.RecordAudit(ctx, "user.created")
})
```

どこで走るかを何も言わないステートメントは既定のグループへ行きます。`pw.SelectDB`、
`pw.SelectWriteDB` が1つを固定します。単一のステートメントでも `pw.Transaction` 全体でも
同じです。生成された関数がトポロジを知ることは無いので、開発用の SQLite ファイル1つが
どのグループ名にも答えられます。
[リレーショナルデータベース](/ja/guides/storage/rdb/)と
[ランタイム API](/ja/reference/runtime/)を参照してください。

## JOIN の行をまとめる

JOIN は子ごとに親の行を返し直すので、どの結果種別の宣言でもその平坦化は元に戻せません。
`sqlbind.ScanRows[T]` があとから木を組み立て直します。対象は任意のクエリで、SQL テンプレート
は関係しません。

```go
type Organization struct {
	ID    int    `db:"organization_id" groupkey:""`
	Name  string `db:"organization_name"`
	Users []User
}

type User struct {
	ID   int    `db:"user_id" groupkey:""`
	Name string `db:"user_name"`
}
```

```go
rows, err := db.QueryContext(ctx, `
SELECT o.id AS organization_id, o.name AS organization_name,
       u.id AS user_id,         u.name AS user_name
FROM organizations o
LEFT JOIN users u ON u.organization_id = o.id
ORDER BY o.id, u.id`)
if err != nil {
	return nil, err
}
defer rows.Close()
return sqlbind.ScanRows[Organization](rows)
```

| 規則 | 内容 |
| --- | --- |
| `groupkey:""` | まとめる構造体の各段にスカラのフィールドをちょうど1つ |
| `db:"alias"` | スカラのフィールドが読むカラム別名。タグが無ければ Go のフィールド名のスネークケース |
| ルートキーが同じ | 行が1つのルートオブジェクトへまとまる |
| 子のキーが同じ | 行が1つの子オブジェクトへまとまる |
| 子のキーが NULL | その子は不在。外部結合が意味するとおり |
| ルートキーが NULL | エラー |

使いどころを決めるのは2つの制約です。`ScanRows` は `database/sql` を使うホストの Go を
対象としていて、**TinyGo のビルドからは除外されます**。そして木を組み立てるために結果の
行をすべて消費するので、非常に大きな結果はメモリに載ります。普通のクエリ——行が1つずつ
流れていくもの——には `sql.one`・`sql.optional`・`sql.many` を、JOIN が同じ親を繰り返し、
その親を丸ごと受け取りたいときには `ScanRows` を使ってください。

## よくあるエラー

生成時:

- 手書きの `$1` や `?` のプレースホルダ
- 結果の型と食い違う SELECT のカラム数や名前
- 条件で増減する SELECT や RETURNING のカラム
- 証明できる `WHERE` を持たない UPDATE や DELETE、`SET` の項目がすべて条件付きの UPDATE
- `{if …}` の `bool` でない条件
- 分岐によってカラム数と値の数が食い違いうる INSERT
- 閉じる `AND` が条件をまたいで分割された `BETWEEN`
- `CASE` の内側で何も出力しえない条件付きの断片
- 分岐ごとに括弧のネストが揃わない本文
- `ctx` または `db` という名前のパラメータ（生成される全関数のコンテキストと実行体）
- 再帰する `sql.relation`
- 小文字で始まる名前の `sql.relation`
- 宣言したファイルの外から使われた `type`、または1つのディレクトリの2つのファイルで宣言された `type`
- ステートメント名の大小と食い違う `export`
- コンポーネントパッケージの中の `.pw.sql`

実行時:

- 展開される値のリストに渡された空のスライス
- `sql.one` に0行または複数行、`sql.optional` に複数行
- `sql.many` を range しながら無視されたクエリエラー

`dev` では生成されたステートメントがすべて所要時間つきで記録され、しきい値より遅いものは
クエリプランと貼り付けて再実行できる断片を伴います。
[クエリー診断](/ja/productivity/query-diagnostics/)を参照してください。
