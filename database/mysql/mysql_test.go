package mysql

import "testing"

func TestWithParseTime(t *testing.T) {
	for _, testCase := range []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "no parameters",
			dsn:  "user:pass@tcp(127.0.0.1:3306)/app",
			want: "user:pass@tcp(127.0.0.1:3306)/app?parseTime=true",
		},
		{
			name: "existing parameters",
			dsn:  "user:pass@tcp(127.0.0.1:3306)/app?charset=utf8mb4",
			want: "user:pass@tcp(127.0.0.1:3306)/app?charset=utf8mb4&parseTime=true",
		},
		{
			// An operator who turned it off meant it, even though the framework
			// would rather it were on.
			name: "explicitly disabled",
			dsn:  "user:pass@tcp(127.0.0.1:3306)/app?parseTime=false",
			want: "user:pass@tcp(127.0.0.1:3306)/app?parseTime=false",
		},
		{
			// A password may contain ?, so the search starts after the last /,
			// which is where the database name begins.
			name: "question mark in the password",
			dsn:  "user:pa?ss@tcp(127.0.0.1:3306)/app",
			want: "user:pa?ss@tcp(127.0.0.1:3306)/app?parseTime=true",
		},
		{
			// A parameter whose name merely ends in parseTime is not the one.
			name: "similar parameter name",
			dsn:  "user:pass@tcp(127.0.0.1:3306)/app?noParseTime=true",
			want: "user:pass@tcp(127.0.0.1:3306)/app?noParseTime=true&parseTime=true",
		},
		{
			// Nothing this function can reason about; the driver reports it.
			name: "malformed",
			dsn:  "not-a-dsn",
			want: "not-a-dsn",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := withParseTime(testCase.dsn); got != testCase.want {
				t.Fatalf("withParseTime(%q) = %q, want %q", testCase.dsn, got, testCase.want)
			}
		})
	}
}
