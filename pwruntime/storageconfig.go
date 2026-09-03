package pwruntime

import (
	"errors"
	"fmt"
	"strings"
)

// Storage backends a bucket may name, per requirement:object-storage.
const (
	// StorageBackendLocal keeps objects in a directory under the project,
	// which is what api:cli-dev and a test run on.
	StorageBackendLocal = "local"
	// StorageBackendS3 reaches an S3-compatible endpoint, including R2's S3
	// API from a process host.
	StorageBackendS3 = "s3"
	// StorageBackendR2 reaches an R2 bucket through a Worker binding, and
	// exists only inside a Cloudflare Worker.
	StorageBackendR2 = "r2"
)

// StorageConfig names the object storage buckets an application addresses.
type StorageConfig struct {
	Enabled bool `default:"false" help:"open the configured object storage buckets"`
	// Buckets is the array-of-tables form. An element takes no CLI option and
	// no environment variable of its own; the array travels as JSON in one
	// variable, per pwconfig.BucketsEnv.
	Buckets []StorageBucketConfig `dependon:".enabled" help:"bucket set, one element per bucket"`
}

// StorageBucketConfig is one bucket of the set. The backend decides which of
// the remaining fields are read; the rest are ignored rather than refused, so
// one file can describe a bucket for two hosts.
type StorageBucketConfig struct {
	// Name is what a call site addresses this bucket by.
	Name string `json:"name" help:"name this bucket is addressed by"`
	// Backend names where objects live.
	Backend string `json:"backend" default:"local" enum:"local,s3,r2" help:"where objects live: local, s3, or r2"`
	// Directory is the local backend's root, relative to the working directory.
	Directory string `json:"directory" help:"local: directory objects are kept in"`
	// Endpoint, Region and Bucket address an S3-compatible store.
	Endpoint string `json:"endpoint" help:"s3: endpoint URL"`
	Region   string `json:"region" help:"s3: signing region"`
	Bucket   string `json:"bucket" help:"s3: bucket name at the endpoint"`
	// AccessKeyID and SecretAccessKey sign S3 requests. A file writes them as
	// ${NAME} references, never inline.
	AccessKeyID     string `json:"access_key_id" secret:"mask" help:"s3: access key id"`
	SecretAccessKey string `json:"secret_access_key" secret:"mask" help:"s3: secret access key"`
	// PathStyle addresses the bucket in the path rather than the host, which
	// MinIO and some proxies need.
	PathStyle bool `json:"path_style" default:"false" help:"s3: put the bucket in the path rather than the host"`
	// Binding is the R2 bucket binding's name in the Worker env.
	Binding string `json:"binding" help:"r2: bucket binding the Worker env carries"`
}

// Validate rejects a set whose buckets cannot be addressed or opened.
func (c StorageConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if len(c.Buckets) == 0 {
		return errors.New("storage.enabled needs at least one [[storage.buckets]] element")
	}
	seen := map[string]bool{}
	for index, bucket := range c.Buckets {
		if strings.TrimSpace(bucket.Name) == "" {
			return fmt.Errorf("storage.buckets[%d]: name is required", index)
		}
		if seen[bucket.Name] {
			return fmt.Errorf("storage.buckets[%d]: duplicate bucket name %q, which a call site could not address", index, bucket.Name)
		}
		seen[bucket.Name] = true
		switch bucket.Backend {
		case "", StorageBackendLocal:
			if strings.TrimSpace(bucket.Directory) == "" {
				return fmt.Errorf("storage.buckets[%d] (%s): local needs directory", index, bucket.Name)
			}
		case StorageBackendS3:
			if strings.TrimSpace(bucket.Bucket) == "" {
				return fmt.Errorf("storage.buckets[%d] (%s): s3 needs bucket", index, bucket.Name)
			}
		case StorageBackendR2:
			if strings.TrimSpace(bucket.Binding) == "" {
				return fmt.Errorf("storage.buckets[%d] (%s): r2 needs binding", index, bucket.Name)
			}
		default:
			return fmt.Errorf("storage.buckets[%d] (%s): backend %q is not local, s3, or r2", index, bucket.Name, bucket.Backend)
		}
	}
	return nil
}
