package dynamo

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	config := DefaultConfig()
	config.Enabled = true
	config.Region = "ap-northeast-1"
	return config
}

func TestValidateAcceptsADisabledSection(t *testing.T) {
	// A disabled section is not a half-configured one, so nothing about it is
	// worth reporting.
	if err := (Config{}).validate(false); err != nil {
		t.Fatalf("a disabled section must validate: %v", err)
	}
}

func TestValidateRejectsHalfConfiguredCredentials(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"key without secret", func(c *Config) { c.AccessKeyID = "AKIA" }, "secret_access_key"},
		{"secret without key", func(c *Config) { c.SecretAccessKey = "s" }, "access_key_id"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			config := validConfig()
			testCase.mutate(&config)
			err := config.validate(false)
			if err == nil {
				t.Fatal("half-configured credentials must be rejected")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error must name the missing key, got %v", err)
			}
			// The values themselves must never reach the message.
			if strings.Contains(err.Error(), "AKIA") {
				t.Fatalf("error leaked a credential: %v", err)
			}
		})
	}
}

func TestValidateRejectsAutoMigrateOutsideDevelopment(t *testing.T) {
	config := validConfig()
	config.AutoMigrate = true
	if err := config.validate(true); err != nil {
		t.Fatalf("auto_migrate must be allowed in development: %v", err)
	}
	err := config.validate(false)
	if err == nil {
		t.Fatal("auto_migrate outside development must be a configuration error, not a warning")
	}
	if !strings.Contains(err.Error(), "auto_migrate") {
		t.Fatalf("error must name the key, got %v", err)
	}
}

func TestValidateRejectsANonPositiveTimeout(t *testing.T) {
	config := validConfig()
	config.Timeout = 0
	if err := config.validate(false); err == nil {
		t.Fatal("a zero timeout must be rejected")
	}
}

func TestValidateRejectsADuplicateTableMapping(t *testing.T) {
	config := validConfig()
	config.TableNames = []TableName{
		{Declared: "reading", Deployed: "a-reading"},
		{Declared: "reading", Deployed: "b-reading"},
	}
	err := config.validate(false)
	if err == nil {
		t.Fatal("one declared table cannot map to two deployed names")
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Fatalf("error must say what is wrong, got %v", err)
	}
}

func TestValidateRejectsAnIllegalPrefix(t *testing.T) {
	config := validConfig()
	config.TablePrefix = "not ok/"
	if err := config.validate(false); err == nil {
		t.Fatal("a prefix outside the DynamoDB character set must be rejected")
	}
}

// TestDefaultConfigMatchesTheBoundDefaults keeps the two statements of the
// same defaults from drifting: the struct tags configbind reads to fill an
// unset key, and DefaultConfig for a caller building a Config in Go.
//
// They drifted once already. DefaultConfig existed while the tags did not, so
// nothing called it and every project with the section enabled failed startup
// on a zero timeout.
func TestDefaultConfigMatchesTheBoundDefaults(t *testing.T) {
	tags := map[string]string{}
	for _, field := range reflect.VisibleFields(reflect.TypeOf(Config{})) {
		if value, tagged := field.Tag.Lookup("default"); tagged {
			tags[field.Name] = value
		}
	}
	defaults := DefaultConfig()
	if defaults.AutoMigrate {
		t.Fatal("auto_migrate must default off")
	}
	want := map[string]string{
		"Timeout":      defaults.Timeout.String(),
		"MaxIdleConns": strconv.Itoa(defaults.MaxIdleConns),
		"VerifySchema": strconv.FormatBool(defaults.VerifySchema),
	}
	for name, value := range want {
		tagged, present := tags[name]
		if !present {
			t.Errorf("%s has no default tag, so configbind leaves it zero", name)
			continue
		}
		parsed, err := time.ParseDuration(tagged)
		if err == nil {
			tagged = parsed.String()
		}
		if tagged != value {
			t.Errorf("%s: tag says %q, DefaultConfig says %q", name, tagged, value)
		}
	}
	// Anything else carrying a tag is a value this test does not know about.
	for name := range tags {
		if _, known := want[name]; !known {
			t.Errorf("%s gained a default tag; add it to DefaultConfig and to this test", name)
		}
	}
}
