package pwconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// BucketsEnv is the environment variable that carries the [[storage.buckets]]
// array as JSON, for the reason ConnectionsEnv carries the connections: an
// array of tables has no per-key environment form, and a Worker has no file.
const BucketsEnv = "STORAGE_BUCKETS"

// EncodeBucketsEnv is the value of BucketsEnv for a bucket list.
func EncodeBucketsEnv(buckets []StorageBucketConfig) (string, error) {
	encoded, err := json.Marshal(buckets)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// DecodeBucketsEnv parses the value of BucketsEnv, resolving ${NAME} in the
// credential and endpoint fields from environ so a secret still arrives as a
// secret.
func DecodeBucketsEnv(value string, environ map[string]string) ([]StorageBucketConfig, error) {
	var buckets []StorageBucketConfig
	if err := json.Unmarshal([]byte(value), &buckets); err != nil {
		return nil, fmt.Errorf("%s: %w", BucketsEnv, err)
	}
	for index := range buckets {
		for _, field := range []*string{&buckets[index].Endpoint, &buckets[index].AccessKeyID, &buckets[index].SecretAccessKey, &buckets[index].Directory} {
			expanded, err := expandEnvRefs(*field, environ)
			if err != nil {
				return nil, fmt.Errorf("%s[%d]: %w", BucketsEnv, index, err)
			}
			*field = expanded
		}
	}
	return buckets, nil
}

// applyBucketsEnv is the environment layer for the storage bucket array.
func applyBucketsEnv(environ []string) error {
	if environ == nil {
		environ = os.Environ()
	}
	values := make(map[string]string, len(environ))
	for _, line := range environ {
		name, value, _ := strings.Cut(line, "=")
		values[name] = value
	}
	raw, ok := values[BucketsEnv]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	buckets, err := DecodeBucketsEnv(raw, values)
	if err != nil {
		return err
	}
	storage := boundConfig[StorageConfig]()
	if storage == nil {
		return nil
	}
	storage.Buckets = buckets
	return nil
}
