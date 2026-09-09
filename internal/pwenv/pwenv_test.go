package pwenv

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDefaultsToDevelopment(t *testing.T) {
	for name, environ := range map[string][]string{
		"unset":      {"PATH=/usr/bin"},
		"empty":      {"APP_ENV="},
		"whitespace": {"APP_ENV=   "},
	} {
		t.Run(name, func(t *testing.T) {
			env, err := Resolve(environ)
			if err != nil {
				t.Fatal(err)
			}
			if env != Development {
				t.Fatalf("Resolve = %q, want %q", env, Development)
			}
		})
	}
}

func TestResolveAcceptsKnownAndCustomTokens(t *testing.T) {
	for _, token := range []string{Development, Staging, Production, "test", "customer-a", "eu_west"} {
		env, err := Resolve([]string{"APP_ENV=" + token})
		if err != nil {
			t.Fatalf("Resolve(%q): %v", token, err)
		}
		if env != token {
			t.Fatalf("Resolve(%q) = %q", token, env)
		}
	}
}

func TestResolveRejectsTokensUnsafeAsFilenameComponent(t *testing.T) {
	for _, token := range []string{"Prod", "pro d", "../etc", "a/b", "dev.local", "prod\n"} {
		if _, err := Resolve([]string{"APP_ENV=" + token}); err == nil {
			t.Fatalf("Resolve(%q) accepted an invalid token", token)
		} else if !strings.Contains(err.Error(), Var) {
			t.Fatalf("Resolve(%q) error does not name %s: %v", token, Var, err)
		}
	}
}

func TestResolveUsesTheLastAssignmentInEnviron(t *testing.T) {
	env, err := Resolve([]string{"APP_ENV=dev", "OTHER=1", "APP_ENV=prod"})
	if err != nil {
		t.Fatal(err)
	}
	if env != Production {
		t.Fatalf("Resolve = %q, want %q", env, Production)
	}
}

func TestReadPathsPrefersWorkingDirectoryOverConfigDirectory(t *testing.T) {
	paths := ReadPaths(Staging)
	want := []string{"config.stg.toml", filepath.Join("config", "config.stg.toml")}
	if len(paths) != len(want) {
		t.Fatalf("ReadPaths = %v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("ReadPaths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestIsFileNameExcludesTheNeutralName(t *testing.T) {
	for _, name := range []string{"config.dev.toml", "config.stg.toml", "config.customer-a.toml"} {
		if !IsFileName(name) {
			t.Errorf("IsFileName(%q) = false", name)
		}
	}
	for _, name := range []string{NeutralFileName, "popcornweb.toml", "config.dev.yaml", "app.dev.toml"} {
		if IsFileName(name) {
			t.Errorf("IsFileName(%q) = true", name)
		}
	}
}

func TestIsDotenvFileNameAcceptsTheFamilyButNotTheTemplate(t *testing.T) {
	for name, want := range map[string]bool{
		".env": true, ".env.local": true, ".env.dev": true, ".env.dev.local": true, ".env.stg": true, ".env.my-env_2": true,
		".env.example": false, ".env.": false, ".env.Dev": false, ".envrc": false, "env": false,
	} {
		if got := IsDotenvFileName(name); got != want {
			t.Errorf("IsDotenvFileName(%q) = %v, want %v", name, got, want)
		}
	}
	if DotenvFileName("prod") != ".env.prod" {
		t.Fatal(DotenvFileName("prod"))
	}
	if got := DotenvFileNames("stg"); len(got) != 4 || got[1] != ".env.local" || got[2] != ".env.stg" || got[3] != ".env.stg.local" {
		t.Fatalf("DotenvFileNames = %v", got)
	}
}
