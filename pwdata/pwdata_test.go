package pwdata

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/shibukawa/popcornweb/database/sqlite"
	"github.com/shibukawa/tinybind-go/sqlbind"
)

func open(t *testing.T) *Connection {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	schema := []string{
		`CREATE TABLE memos (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT)`,
		`CREATE TABLE tags (memo_id INTEGER, name TEXT)`,
		`CREATE TABLE popcornweb_session (id TEXT PRIMARY KEY, payload TEXT)`,
		`INSERT INTO memos (id, title, body) VALUES (1, 'first', 'one'), (2, 'second', NULL)`,
		`INSERT INTO tags (memo_id, name) VALUES (1, 'work')`,
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	return NewSingle(db, "sqlite", "dev").Default()
}

func TestTablesAreListedAndFrameworkOnesMarked(t *testing.T) {
	tables, err := open(t).Tables(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, table := range tables {
		found[table.Name] = table.Framework
	}
	if _, ok := found["memos"]; !ok {
		t.Fatalf("tables = %v, want the application table", found)
	}
	if found["memos"] {
		t.Error("an application table was marked as framework-owned")
	}
	// The prefix is the rule an application reads its own schema by, so the
	// pane reads it the same way.
	if !found["popcornweb_session"] {
		t.Error("a framework table was not marked")
	}
}

func TestColumnsCarryTypesAndTheKey(t *testing.T) {
	columns, err := open(t).Columns(context.Background(), "memos")
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 3 {
		t.Fatalf("columns = %+v, want three", columns)
	}
	if columns[0].PrimaryKey != 1 {
		t.Errorf("id = %+v, want the first key column", columns[0])
	}
	if !columns[1].NotNull {
		t.Errorf("title = %+v, want not null", columns[1])
	}
}

func TestRowsArePagedAndOrderedByKey(t *testing.T) {
	page, err := open(t).Rows(context.Background(), "memos", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Ordered {
		t.Error("a keyed table was not reported as ordered")
	}
	if len(page.Rows) != 2 {
		t.Fatalf("rows = %v, want two", page.Rows)
	}
	// NULL and the empty string are the distinction a developer opens this
	// pane to check, so they must not render the same.
	if page.Rows[1][2] != nil {
		t.Errorf("body = %v, want NULL preserved as nil", *page.Rows[1][2])
	}
}

func TestTableWithoutAKeySaysItsOrderIsUnspecified(t *testing.T) {
	page, err := open(t).Rows(context.Background(), "tags", 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Ordered {
		t.Error("a table with no primary key claimed a stable order")
	}
}

// The catalog is the allow list. A name carrying SQL is refused rather than
// quoted and hoped about.
func TestUnknownTableIsRefused(t *testing.T) {
	_, err := open(t).Rows(context.Background(), "memos; DROP TABLE memos", 0)
	if err == nil {
		t.Fatal("a table name the catalog does not report was accepted")
	}
	if !strings.Contains(err.Error(), "no table named") {
		t.Errorf("err = %v, want the name refused", err)
	}
}

func TestUpdateRowWritesByPrimaryKey(t *testing.T) {
	connection := open(t)
	affected, err := connection.UpdateRow(context.Background(), RowEdit{
		Table:  "memos",
		Key:    map[string]string{"id": "1"},
		Values: map[string]string{"title": "renamed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if affected != 1 {
		t.Errorf("affected = %d, want 1", affected)
	}
	page, _ := connection.Rows(context.Background(), "memos", 0)
	if *page.Rows[0][1] != "renamed" {
		t.Errorf("title = %q, want the new value", *page.Rows[0][1])
	}
}

func TestUpdateCanSetAColumnToNull(t *testing.T) {
	connection := open(t)
	if _, err := connection.UpdateRow(context.Background(), RowEdit{
		Table: "memos", Key: map[string]string{"id": "1"}, Nulls: []string{"body"},
	}); err != nil {
		t.Fatal(err)
	}
	page, _ := connection.Rows(context.Background(), "memos", 0)
	if page.Rows[0][2] != nil {
		t.Errorf("body = %q, want NULL", *page.Rows[0][2])
	}
}

func TestInsertAndDeleteRow(t *testing.T) {
	connection := open(t)
	if _, err := connection.InsertRow(context.Background(), RowEdit{
		Table:  "memos",
		Values: map[string]string{"id": "3", "title": "third"},
	}); err != nil {
		t.Fatal(err)
	}
	page, _ := connection.Rows(context.Background(), "memos", 0)
	if len(page.Rows) != 3 {
		t.Fatalf("rows = %d, want three after the insert", len(page.Rows))
	}
	if _, err := connection.DeleteRow(context.Background(), RowEdit{
		Table: "memos", Key: map[string]string{"id": "3"},
	}); err != nil {
		t.Fatal(err)
	}
	page, _ = connection.Rows(context.Background(), "memos", 0)
	if len(page.Rows) != 2 {
		t.Errorf("rows = %d, want two after the delete", len(page.Rows))
	}
}

// Without a key there is no way to address one row, and saying so beats
// changing an unknown number of them.
func TestEditingAKeylessTableIsRefused(t *testing.T) {
	_, err := open(t).UpdateRow(context.Background(), RowEdit{
		Table: "tags", Values: map[string]string{"name": "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "no primary key") {
		t.Errorf("err = %v, want the missing key explained", err)
	}
}

func TestUnknownColumnIsRefused(t *testing.T) {
	_, err := open(t).UpdateRow(context.Background(), RowEdit{
		Table: "memos", Key: map[string]string{"id": "1"},
		Values: map[string]string{"nope": "x"},
	})
	if err == nil || !strings.Contains(err.Error(), "no column named") {
		t.Errorf("err = %v, want the column refused", err)
	}
}

func TestConsoleRunsAStatementAndReportsRows(t *testing.T) {
	result := open(t).Exec(context.Background(), "SELECT id, title FROM memos ORDER BY id")
	if result.Error != "" {
		t.Fatalf("error = %s", result.Error)
	}
	if !result.Returned || len(result.Rows) != 2 {
		t.Fatalf("result = %+v, want two rows", result)
	}
}

func TestConsoleReportsAffectedRowsForAWrite(t *testing.T) {
	result := open(t).Exec(context.Background(), "UPDATE memos SET title = 'x'")
	if result.Error != "" {
		t.Fatalf("error = %s", result.Error)
	}
	if result.Returned {
		t.Error("a write was reported as returning rows")
	}
	if result.Affected != 2 {
		t.Errorf("affected = %d, want 2", result.Affected)
	}
}

func TestConsoleReportsAFailingStatement(t *testing.T) {
	result := open(t).Exec(context.Background(), "SELECT * FROM nope")
	if result.Error == "" {
		t.Error("a failing statement was reported as success")
	}
}

func TestDeclaredQueryRunsTheGeneratedStatement(t *testing.T) {
	registry.Lock()
	registry.queries = nil
	registry.Unlock()
	RegisterQuery(Query{
		Package: "queries", Name: "findMemo", Exported: false,
		Params: []Param{{Name: "id", Kind: "int"}},
		Build: func(args []string) (sqlbind.Statement, error) {
			builder := sqlbind.NewBuilder(sqlbind.Question)
			builder.WriteString("SELECT title FROM memos WHERE id = ")
			builder.Arg(args[0])
			return builder.Statement(), nil
		},
	})
	result := open(t).RunQuery(context.Background(), "queries", "findMemo", []string{"2"})
	if result.Error != "" {
		t.Fatalf("error = %s", result.Error)
	}
	if len(result.Rows) != 1 || *result.Rows[0][0] != "second" {
		t.Fatalf("result = %+v, want the second memo", result)
	}
	// The statement shown is the one the generated builder produced, so what
	// the pane ran and what the application would run are the same text.
	if !strings.Contains(result.SQL, "FROM memos") {
		t.Errorf("SQL = %q, want the built statement", result.SQL)
	}
}

func TestPagesRender(t *testing.T) {
	connection := open(t)
	registry.Lock()
	registry.queries = nil
	registry.Unlock()
	for _, path := range []string{"/", "/table/memos", "/table/tags", "/console", "/queries"} {
		recorder := httptest.NewRecorder()
		serverFor(connection).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s: status = %d", path, recorder.Code)
			continue
		}
		if body := recorder.Body.String(); strings.Contains(body, "template error") {
			t.Errorf("%s failed to render:\n%s", path, body)
		}
		if body := recorder.Body.String(); !strings.Contains(body, "memos") {
			t.Errorf("%s: the sidebar listed no tables", path)
		}
	}
}

func TestEditFormAppliesAnUpdate(t *testing.T) {
	connection := open(t)
	form := url.Values{
		"action":      {"update"},
		"key.id":      {"1"},
		"value.title": {"through the form"},
		"offset":      {"0"},
	}
	request := httptest.NewRequest(http.MethodPost, "/table/memos/row", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	serverFor(connection).Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want a redirect back to the table", recorder.Code)
	}
	page, _ := connection.Rows(context.Background(), "memos", 0)
	if *page.Rows[0][1] != "through the form" {
		t.Errorf("title = %q, want the posted value", *page.Rows[0][1])
	}
}

func TestDialectsDifferWhereTheyMust(t *testing.T) {
	for _, engine := range []struct {
		driver, quoted, marker string
	}{
		{"sqlite", `"x"`, "?"},
		{"postgres", `"x"`, "$1"},
		{"pgx", `"x"`, "$1"},
		{"mysql", "`x`", "?"},
	} {
		d := dialectFor(engine.driver)
		if got := d.quote("x"); got != engine.quoted {
			t.Errorf("%s quote = %s, want %s", engine.driver, got, engine.quoted)
		}
		if got := d.placeholder(1); got != engine.marker {
			t.Errorf("%s placeholder = %s, want %s", engine.driver, got, engine.marker)
		}
		if d.tables == "" || d.columns == "" {
			t.Errorf("%s has no catalog statements", engine.driver)
		}
	}
}

// serverFor wraps one connection the way the framework wraps the application's.
func serverFor(connection *Connection) *Server {
	return New([]Connection{*connection}, "dev")
}

func TestDefaultConnectionIsWritable(t *testing.T) {
	writable := NewConnection("primary#0", "primary", "sqlite", false, nil)
	replica := NewConnection("replica#0", "replica", "sqlite", true, nil)
	// Declaration order puts the replica first on purpose: the default is
	// chosen by what it can do, not by where it sits.
	server := New([]Connection{replica, writable}, "dev")
	if got := server.Default(); got.Label != "primary#0" {
		t.Errorf("default = %q, want the writable connection", got.Label)
	}
	// A project whose connections are all replicas is still readable.
	only := New([]Connection{replica}, "dev")
	if got := only.Default(); got.Label != "replica#0" {
		t.Errorf("default = %q, want the only connection", got.Label)
	}
}

func TestLookupFallsBackToTheDefault(t *testing.T) {
	server := New([]Connection{
		NewConnection("primary#0", "primary", "sqlite", false, nil),
		NewConnection("replica#0", "replica", "postgres", true, nil),
	}, "dev")
	if got := server.Lookup("replica#0"); got.Label != "replica#0" {
		t.Errorf("lookup = %q, want the named connection", got.Label)
	}
	// A stale bookmark should land somewhere usable rather than error.
	if got := server.Lookup("gone#3"); got.Label != "primary#0" {
		t.Errorf("lookup = %q, want the default", got.Label)
	}
}

// The driver is per connection, so two groups on two engines each get their own
// dialect rather than sharing one resolved for the project.
func TestDialectIsResolvedPerConnection(t *testing.T) {
	postgres := NewConnection("reporting#0", "reporting", "postgres", true, nil)
	sqlite := NewConnection("primary#0", "primary", "sqlite", false, nil)
	if postgres.Engine() != "postgres" || sqlite.Engine() != "sqlite" {
		t.Errorf("engines = %q and %q, want each connection's own", postgres.Engine(), sqlite.Engine())
	}
}

func TestMigrationStateReadsTheAppliedVersion(t *testing.T) {
	connection := open(t)
	if state, err := connection.MigrationState(context.Background()); err != nil || state.Present {
		t.Fatalf("state = %+v, err = %v; want absent before any migration", state, err)
	}
	for _, statement := range []string{
		`CREATE TABLE goose_db_version (id INTEGER PRIMARY KEY, version_id INTEGER, is_applied BOOLEAN, tstamp TIMESTAMP)`,
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (0, 1), (1, 1), (2, 1)`,
	} {
		if _, err := connection.db.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	state, err := connection.MigrationState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Present || state.Version != 2 || state.Applied != 3 {
		t.Errorf("state = %+v, want version 2 with three applied", state)
	}
}

// Writing through a replica is refused with the reason, rather than left to the
// engine to report in its own words.
func TestWriteThroughAReadOnlyConnectionIsRefused(t *testing.T) {
	connection := open(t)
	replica := NewConnection("replica#0", "replica", "sqlite", true, connection.db)
	server := New([]Connection{replica}, "dev")
	form := url.Values{"action": {"update"}, "key.id": {"1"}, "value.title": {"x"}, "offset": {"0"}}
	request := httptest.NewRequest(http.MethodPost, "/table/memos/row?c=replica#0", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if location := recorder.Header().Get("Location"); !strings.Contains(location, "read-only") {
		t.Errorf("location = %q, want the refusal explained", location)
	}
	page, _ := connection.Rows(context.Background(), "memos", 0)
	if *page.Rows[0][1] != "first" {
		t.Errorf("title = %q, want it unchanged", *page.Rows[0][1])
	}
}

func TestExplainReadsAPlanWithoutRunningTheStatement(t *testing.T) {
	connection := open(t)
	result := connection.Explain(context.Background(), "UPDATE memos SET title = 'exploded'")
	if result.Error != "" {
		t.Fatalf("error = %s", result.Error)
	}
	if !result.Returned {
		t.Errorf("result = %+v, want plan rows", result)
	}
	// ANALYZE is never used, so the statement whose plan was read did not run.
	page, _ := connection.Rows(context.Background(), "memos", 0)
	if *page.Rows[0][1] == "exploded" {
		t.Error("explaining a write executed it")
	}
}

func TestExplainOfADeclaredQueryUsesTheBuiltStatement(t *testing.T) {
	registry.Lock()
	registry.queries = nil
	registry.Unlock()
	RegisterQuery(Query{
		Package: "queries", Name: "byID", Exported: true,
		Params: []Param{{Name: "id", Kind: "int"}},
		Build: func(args []string) (sqlbind.Statement, error) {
			builder := sqlbind.NewBuilder(sqlbind.Question)
			builder.WriteString("SELECT title FROM memos WHERE id = ")
			builder.Arg(args[0])
			return builder.Statement(), nil
		},
	})
	result := open(t).ExplainQuery(context.Background(), "queries", "byID", []string{"1"})
	if result.Error != "" {
		t.Fatalf("error = %s", result.Error)
	}
	if !strings.Contains(result.SQL, "EXPLAIN") || !strings.Contains(result.SQL, "FROM memos") {
		t.Errorf("SQL = %q, want the built statement explained", result.SQL)
	}
}

// An engine with no plan-only form loses the plan and nothing else.
func TestExplainOnAnEngineWithoutOneSaysSo(t *testing.T) {
	if _, ok := explainPrefix("sqlite"); !ok {
		t.Fatal("sqlite should have a plan form")
	}
	if _, ok := explainPrefix("cockroach"); ok {
		t.Error("an unknown engine should have no plan form")
	}
}

func TestForeignKeysAreDiscoveredAndFollowable(t *testing.T) {
	connection := open(t)
	for _, statement := range []string{
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, memo_id INTEGER REFERENCES memos(id), text TEXT)`,
		`INSERT INTO notes (id, memo_id, text) VALUES (1, 2, 'about the second')`,
	} {
		if _, err := connection.db.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	keys := connection.ForeignKeys(context.Background(), "notes")
	key, ok := keys["memo_id"]
	if !ok {
		t.Fatalf("keys = %+v, want the reference from memo_id", keys)
	}
	if key.Table != "memos" || key.Target != "id" {
		t.Errorf("key = %+v, want memos.id", key)
	}
	// Following it is a selection: the column and table came from the catalog
	// and the value is a bind parameter.
	page, err := connection.Referenced(context.Background(), key.Table, key.Target, "2")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || *page.Rows[0][1] != "second" {
		t.Fatalf("page = %+v, want the referenced row", page.Rows)
	}
}

func TestFollowingAnUnknownColumnIsRefused(t *testing.T) {
	_, err := open(t).Referenced(context.Background(), "memos", "nope", "1")
	if err == nil || !strings.Contains(err.Error(), "no column named") {
		t.Errorf("err = %v, want the column refused", err)
	}
}

// A table with no references is not an error; the grid is simply not linked.
func TestATableWithNoForeignKeysYieldsNone(t *testing.T) {
	if keys := open(t).ForeignKeys(context.Background(), "memos"); len(keys) != 0 {
		t.Errorf("keys = %+v, want none", keys)
	}
}

// The link is what makes an identifier navigable, so its absence is a defect
// rather than a missing nicety.
func TestTheGridLinksAForeignKey(t *testing.T) {
	connection := open(t)
	for _, statement := range []string{
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, memo_id INTEGER REFERENCES memos(id), text TEXT)`,
		`INSERT INTO notes (id, memo_id, text) VALUES (1, 2, 'about the second')`,
	} {
		if _, err := connection.db.ExecContext(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	recorder := httptest.NewRecorder()
	serverFor(connection).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/table/notes", nil))
	body := recorder.Body.String()
	if !strings.Contains(body, `/referenced/memos?`) || !strings.Contains(body, "value=2") {
		t.Errorf("the grid did not link the foreign key:\n%s", body)
	}
}

// The console strips the mount before the request arrives, so a link written as
// an absolute path resolves against the console root and misses. Every link the
// pane writes has to carry the mount the console named.
func TestLinksCarryTheMountTheConsoleNamed(t *testing.T) {
	connection := open(t)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(panePrefixHeader, "/data")
	recorder := httptest.NewRecorder()
	serverFor(connection).Handler().ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if !strings.Contains(body, `href="/data/table/memos`) {
		t.Errorf("a table link did not carry the mount:\n%s", body)
	}
	if !strings.Contains(body, `href="/data/console`) {
		t.Errorf("the console link did not carry the mount:\n%s", body)
	}
	// Reached through a console, the pane offers the way back to it.
	if !strings.Contains(body, `href="/"`) {
		t.Errorf("the pane offers no way back to the console:\n%s", body)
	}
}

// Reached directly, the pane is at the root and adds nothing.
func TestLinksAreUnprefixedWithoutAConsole(t *testing.T) {
	recorder := httptest.NewRecorder()
	serverFor(open(t)).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if body := recorder.Body.String(); !strings.Contains(body, `href="/table/memos`) {
		t.Errorf("a directly reached pane should link plainly:\n%s", body)
	}
}

func TestBatchSaveAppliesEditsAndDeletes(t *testing.T) {
	connection := open(t)
	server := serverFor(connection)
	batch := `{"edits":[{"key":{"id":"1"},"values":{"title":"saved"}}],"deletes":[{"key":{"id":"2"}}]}`
	request := httptest.NewRequest(http.MethodPost, "/table/memos/rows", strings.NewReader(batch))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d (%s), want 204", recorder.Code, recorder.Body)
	}
	page, _ := connection.Rows(context.Background(), "memos", 0)
	if len(page.Rows) != 1 || *page.Rows[0][1] != "saved" {
		t.Errorf("rows = %+v, want one edited row", page.Rows)
	}
}

// A refused write is reported in the engine's own words and leaves the page to
// decide what to do, rather than being swallowed into a reload.
func TestBatchSaveReportsARefusal(t *testing.T) {
	connection := open(t)
	replica := NewConnection("replica#0", "replica", "sqlite", true, connection.db)
	server := New([]Connection{replica}, "dev")
	request := httptest.NewRequest(http.MethodPost, "/table/memos/rows?c=replica#0",
		strings.NewReader(`{"edits":[{"key":{"id":"1"},"values":{"title":"x"}}]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code == http.StatusNoContent {
		t.Fatal("a write through a replica was accepted")
	}
	if !strings.Contains(recorder.Body.String(), "read-only") {
		t.Errorf("body = %q, want the reason", recorder.Body)
	}
}

// The key travels with the row, because it is what identifies the row on the
// server and the browser has no business deciding what that is.
func TestRowCarriesItsPrimaryKey(t *testing.T) {
	columns := []Column{{Name: "id", PrimaryKey: 1}, {Name: "title"}}
	value, title := "7", "x"
	if got := string(keyJSON(columns, []*string{&value, &title})); got != `{"id":"7"}` {
		t.Errorf("key = %s, want only the key column", got)
	}
}

func TestTablePageOffersSchemaAndDataTabs(t *testing.T) {
	recorder := httptest.NewRecorder()
	serverFor(open(t)).Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/table/memos", nil))
	body := recorder.Body.String()
	for _, want := range []string{`data-panel="schema"`, `data-panel="data"`, `id="filter"`, `class="sortable"`, `id="save"`, `class="rowsave"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the table page is missing %q", want)
		}
	}
	// The column type is on the header, where a developer hovers to ask.
	if !strings.Contains(body, `title="TEXT · not null"`) {
		t.Errorf("a column header carries no type:\n%s", body)
	}
	if got := strings.Count(body, `data-new="1"`); got != 2 {
		t.Errorf("blank rows = %d, want one at each end", got)
	}
	// The old stacked form is gone; the grid is the only place to insert.
	if strings.Contains(body, "Insert a row") {
		t.Error("the separate insert form is still rendered")
	}
}

func TestBatchSaveInserts(t *testing.T) {
	connection := open(t)
	batch := `{"inserts":[{"values":{"id":"9","title":"typed in the grid"}}]}`
	request := httptest.NewRequest(http.MethodPost, "/table/memos/rows", strings.NewReader(batch))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	serverFor(connection).Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d (%s), want 204", recorder.Code, recorder.Body)
	}
	page, _ := connection.Rows(context.Background(), "memos", 0)
	if len(page.Rows) != 3 {
		t.Fatalf("rows = %d, want the inserted row", len(page.Rows))
	}
	// A column left blank took the default rather than an empty string.
	if page.Rows[2][2] != nil {
		t.Errorf("body = %q, want the column default", *page.Rows[2][2])
	}
}
