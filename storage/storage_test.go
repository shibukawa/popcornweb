package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/pwruntime"
)

func TestValidateNamesTheMissingBackendImport(t *testing.T) {
	config := pwruntime.StorageConfig{Enabled: true, Buckets: []pwruntime.StorageBucketConfig{{Name: "uploads", Backend: "s3", Bucket: "b"}}}
	err := Validate(config)
	if err == nil || !strings.Contains(err.Error(), "github.com/shibukawa/popcornweb/storage/s3") {
		t.Errorf("missing backend not named: %v", err)
	}
	config.Buckets[0].Backend = "r2"
	config.Buckets[0].Binding = "B"
	if err := Validate(config); err == nil || !strings.Contains(err.Error(), "cloudflare/r2") {
		t.Errorf("r2 import not named: %v", err)
	}
	if err := Validate(pwruntime.StorageConfig{}); err != nil {
		t.Errorf("disabled storage refused: %v", err)
	}
	if err := Validate(pwruntime.StorageConfig{Enabled: true}); err == nil {
		t.Error("an empty enabled set was accepted")
	}
	if err := Validate(pwruntime.StorageConfig{Enabled: true, Buckets: []pwruntime.StorageBucketConfig{{Name: "a", Backend: "local", Directory: "x"}, {Name: "a", Backend: "local", Directory: "y"}}}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate name accepted: %v", err)
	}
}

func TestOpenRefusesWhenStorageIsOff(t *testing.T) {
	if _, err := Open(context.Background(), "uploads"); err == nil || !strings.Contains(err.Error(), "storage.enabled") {
		t.Errorf("open with storage off: %v", err)
	}
}
