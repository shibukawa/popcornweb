package testutil

import (
	"context"
	"fmt"
	"strings"

	"github.com/shibukawa/popcornweb/internal/dbseed"
	"github.com/shibukawa/popcornweb/pw"
)

// WithSeed loads dataset files into the copied database after the migration
// schema is installed and before the HTTP server starts.
//
// Each name is a path relative to the seed directory; the .yaml extension may
// be omitted. Datasets are applied in the given order.
func WithSeed(files ...string) RunOption {
	return func(settings *runSettings) error {
		if len(files) == 0 {
			return fmt.Errorf("testutil: WithSeed requires at least one dataset")
		}
		for _, file := range files {
			if strings.TrimSpace(file) == "" {
				return fmt.Errorf("testutil: empty dataset name")
			}
		}
		settings.seedFiles = append(settings.seedFiles, files...)
		return nil
	}
}

// WithSeedDir overrides the dataset directory, which defaults to testdata/seed
// relative to the test package directory.
func WithSeedDir(directory string) RunOption {
	return func(settings *runSettings) error {
		if strings.TrimSpace(directory) == "" {
			return fmt.Errorf("testutil: empty seed directory")
		}
		settings.seedDir = directory
		return nil
	}
}

// Seed loads dataset files into the running server's database.
//
// Use it to reset state between phases of one test. A failure stops the test.
//
// Under WithTransaction it seeds inside the test transaction, so the rows are
// visible to requests and disappear with the rollback. Otherwise it seeds
// through the pool and the rows are committed.
func (server *Server) Seed(t TestingT, files ...string) {
	t.Helper()
	if len(files) == 0 {
		t.Fatalf("testutil: Seed requires at least one dataset")
		return
	}
	exec, inTransaction := server.executor()
	if err := applySeed(server.Config, exec, inTransaction, server.seedDir, files); err != nil {
		t.Fatalf("seed Popcorn Web database: %v", err)
	}
}

// AssertDB compares the running server's database against expected datasets.
//
// A mismatch is reported through Errorf with a plain-text per-table diff and
// the test continues.
//
// Under WithTransaction it reads inside the test transaction, so writes made by
// requests are visible before any commit. Otherwise only committed state is
// visible, and a request whose transaction is still open has not been compared
// yet.
func (server *Server) AssertDB(t TestingT, files ...string) {
	t.Helper()
	if len(files) == 0 {
		t.Fatalf("testutil: AssertDB requires at least one dataset")
		return
	}
	dialect, paths, err := resolveSeed(server.Config, server.seedDir, files)
	if err != nil {
		t.Fatalf("assert Popcorn Web database: %v", err)
		return
	}
	exec, inTransaction := server.executor()
	matched, report, err := dbseed.Assert(context.Background(), exec, dialect, inTransaction, paths)
	if err != nil {
		t.Fatalf("assert Popcorn Web database: %v", err)
		return
	}
	if !matched {
		t.Errorf("Popcorn Web database does not match:\n%s", report)
	}
}

// executor selects the statement target for mid-test seeding and assertion.
// Under WithTransaction that is the test transaction — a *sql.Tx or the
// native one, whichever kind of pool backs the connection — so uncommitted
// request writes are visible and seeded rows roll back with it.
func (server *Server) executor() (dbseed.Executor, bool) {
	if server.transaction {
		if executor := server.scope.ActiveExecutor(); executor != nil {
			return dbseed.FromRuntime(executor), true
		}
	}
	if server.DB == nil {
		return nil, false
	}
	return dbseed.FromSQL(server.DB), false
}

func applySeed(config *Config, exec dbseed.Executor, inTransaction bool, directory string, files []string) error {
	dialect, paths, err := resolveSeed(config, directory, files)
	if err != nil {
		return err
	}
	if exec == nil {
		return fmt.Errorf("configured RDB is disabled")
	}
	return dbseed.Apply(context.Background(), exec, dialect, inTransaction, paths)
}

func resolveSeed(config *Config, directory string, files []string) (dbseed.Dialect, []string, error) {
	middleware := config.Get[pw.MiddlewareConfig]()
	if !middleware.RDB.Enabled {
		return "", nil, fmt.Errorf("configured RDB is disabled")
	}
	// Seed data lands where the schema does, which is the migration group
	// rather than the default one.
	dsn, err := middleware.RDB.MigrationDSN()
	if err != nil {
		return "", nil, err
	}
	dialect, err := dbseed.ResolveDialect(dsn)
	if err != nil {
		return "", nil, err
	}
	if directory == "" {
		directory = dbseed.DefaultDir
	}
	paths, err := dbseed.Resolve(directory, files)
	if err != nil {
		return "", nil, err
	}
	return dialect, paths, nil
}
