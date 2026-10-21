# 外部で用意したDB接続の受け入れ

pgmemなどのDBをテストケースの外側で準備し、そのプールをアプリへ渡せるようにする。
未追跡で残っていた `FromPool` と関連テストを取り込み、欠けていた実装を補完した。
要件と受け入れ条件は `.knowledge/concepts/requirement/external-database-injection.yaml` に記録する。

## APIと所有権

- `postgres.FromPool(pool)`：呼び出し側が所有するpgxプールをnative接続に変換する。
- `pw.WithDatabase(connections...)`：設定DSNへ接続せず、渡された接続群をアプリの起動に使う。
- `pwdatabase.Install(set)`：プロセスの接続群を差し替える。以前フレームワークが開いた接続群だけを閉じる。
- `testutil.WithDatabase(connection)`：TestRunのローカルな接続を指定する。プロセス全体の接続群は変更しない。

呼び出し側のプールは、スタックの差し替え、テスト終了、初期化失敗のいずれでも閉じない。
TestRunがnative接続のために追加した `database/sql` のツール用ハンドルはTestRunが閉じる。
`Connection.DataSource` はツールが同じDBへ接続するためのURLで、認証情報を含み得るためログへ出さない。

RDBの有効化は明示的に必要。TestRunではコピーした接続構成を渡された1接続から作るため、
設定ファイルのDSNは不要。通常起動の `pw.WithDatabase` は既存の接続構成の検証を維持する。
グループ名は構成と合わせ、複数グループの場合は `default_group` を指定する。
プロセス全体の差し替えは、リクエスト受付前に直列で行う。

空の接続、無効なRDB、DriverとDataSourceの不一致、native接続のツール用DSN不足を拒否する。
SQLite以外のSQLハンドルも、マイグレーションを使う場合はDataSourceが必要。
設定の解析後に変更したpgxの接続設定や独自ダイヤラーはURLから復元しない。
その場合はツールが同じDBへ到達できるDataSourceを明示する。

## pgmemの利用

`examples/pgmem` は独立したGoモジュールで、pgmem v1.18.2を使う。
pgmemをフレームワーク本体の依存には追加していない。
`TestMain` のfixtureがDBとプールを用意し、各TestRunへ渡す。
fixtureが最後にDBとプールを終了する。

通常のpgx接続、`WithConn`、生成クエリと同じexecutor経路、トランザクションをそのまま利用できる。
マイグレーション・seed・AssertDBも同じDBに対して動き、
`WithTransaction(true)` ではアプリのトランザクションがセーブポイントになり、終了時にロールバックする。

## 検証

- 全Goテストと `go vet ./...` が成功。
- 関連パッケージの通常・pwdevタグのテストが成功。
- 一時GOBINへの `go install ./cmd/pw` と `pw version` が成功。
- pgmem統合テストで、HTTP・マイグレーション・seed・AssertDB・nativeクエリ・トランザクションを確認。
- 同時に存在する2サーバーのDB分離、終了時のロールバック、呼び出し側プールの生存とツール用ハンドルの解放を確認。
- `t.Parallel()` で外部fixtureの異なるforkを使い、DBが混ざらないことを確認。
- 初期化失敗時の所有権維持、DSN不足時に設定先や仮の接続先へ接続しないことを確認。
- 日英のテストガイドを更新し、サイト205ページのビルドを確認。

他の未追跡作業は変更せず、PR用ブランチで実装した。pgmem自体のライフサイクル管理や、
fasthttpの新しい起動オプションは対象に含めない。fasthttpでは既存のRuntimeOptions.Resourcesが接続群を受け取れる。
