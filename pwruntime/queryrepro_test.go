package pwruntime

import (
	"strings"
	"testing"
	"time"
)

func TestReproductionSnippetBindsInsteadOfInlining(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		query  string
		args   []any
		want   string
	}{
		{
			name:   "sqlite binds by parameter name",
			driver: "sqlite",
			query:  "SELECT name FROM items WHERE name = $1 AND rank > $2",
			args:   []any{"alpha", 3},
			want:   ".parameter set $1 'alpha'\n.parameter set $2 3\nSELECT name FROM items WHERE name = $1 AND rank > $2;",
		},
		{
			name:   "postgres prepares the statement",
			driver: "postgres",
			query:  "SELECT name FROM items WHERE name = $1",
			args:   []any{"alpha"},
			want:   "PREPARE pw_repro AS SELECT name FROM items WHERE name = $1; EXECUTE pw_repro('alpha'); DEALLOCATE pw_repro;",
		},
		{
			name:   "mysql routes values through user variables",
			driver: "mysql",
			query:  "SELECT name FROM items WHERE name = ?",
			args:   []any{"alpha"},
			want:   "SET @p1 = 'alpha'; PREPARE pw_repro FROM 'SELECT name FROM items WHERE name = ?'; EXECUTE pw_repro USING @p1; DEALLOCATE PREPARE pw_repro;",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := reproductionSnippet(test.driver, test.query, test.args)
			if got != test.want {
				t.Errorf("snippet =\n%q\nwant\n%q", got, test.want)
			}
		})
	}
}

func TestReproductionSnippetRefusesUnreproducibleInput(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		query  string
		args   []any
	}{
		{name: "unknown driver", driver: "cockroach", query: "SELECT $1", args: []any{"a"}},
		{name: "no arguments", driver: "sqlite", query: "SELECT 1", args: nil},
		{name: "placeholder style mismatch", driver: "sqlite", query: "SELECT ?", args: []any{"a"}},
		{name: "mysql with dollar placeholders", driver: "mysql", query: "SELECT $1", args: []any{"a"}},
		{name: "placeholder count mismatch", driver: "postgres", query: "SELECT $1", args: []any{"a", "b"}},
		{name: "control character in value", driver: "sqlite", query: "SELECT $1", args: []any{"a\nb"}},
		{name: "unsupported value type", driver: "sqlite", query: "SELECT $1", args: []any{struct{}{}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := reproductionSnippet(test.driver, test.query, test.args); got != "" {
				t.Errorf("want no snippet, got %q", got)
			}
		})
	}
}

func TestReproductionSnippetQuotesValues(t *testing.T) {
	stamp := time.Date(2026, 7, 29, 10, 30, 0, 0, time.UTC)
	got := reproductionSnippet("postgres",
		"INSERT INTO t VALUES ($1, $2, $3, $4, $5)",
		[]any{"it's", []byte{0xde, 0xad}, true, nil, stamp})
	for _, want := range []string{"'it''s'", "'\\xdead'::bytea", "TRUE", "NULL", "'2026-07-29T10:30:00Z'"} {
		if !strings.Contains(got, want) {
			t.Errorf("snippet %q missing %q", got, want)
		}
	}
}

func TestReproductionSnippetBoolPerDialect(t *testing.T) {
	sqlite := reproductionSnippet("sqlite", "SELECT $1", []any{true})
	if !strings.Contains(sqlite, ".parameter set $1 1") {
		t.Errorf("sqlite bool = %q, want 1", sqlite)
	}
	postgres := reproductionSnippet("postgres", "SELECT $1", []any{false})
	if !strings.Contains(postgres, "EXECUTE pw_repro(FALSE)") {
		t.Errorf("postgres bool = %q, want FALSE", postgres)
	}
}

func TestScanPlaceholdersIgnoresLiteralsAndComments(t *testing.T) {
	dollars, questions := scanPlaceholders(
		"SELECT '?', \"$9\" -- $8 ?\n, /* $7 ? */ x FROM t WHERE a = $1 AND b = ?")
	if len(dollars) != 1 || !dollars[1] {
		t.Errorf("dollars = %v, want only $1", dollars)
	}
	if questions != 1 {
		t.Errorf("questions = %d, want 1", questions)
	}
}
