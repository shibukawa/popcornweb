package pwconfig

import (
	"strings"
	"testing"
)

func TestBucketsEnvRoundTripsAndExpandsSecrets(t *testing.T) {
	encoded, err := EncodeBucketsEnv([]StorageBucketConfig{
		{Name: "uploads", Backend: "s3", Endpoint: "https://${R2_ACCOUNT}.r2.cloudflarestorage.com", Bucket: "uploads", AccessKeyID: "${R2_KEY}", SecretAccessKey: "${R2_SECRET}"},
		{Name: "media", Backend: "r2", Binding: "MEDIA"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "k-1") {
		t.Fatal("the encoded form carries an expanded secret")
	}
	buckets, err := DecodeBucketsEnv(encoded, map[string]string{"R2_ACCOUNT": "acct", "R2_KEY": "k-1", "R2_SECRET": "s-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 || buckets[0].Endpoint != "https://acct.r2.cloudflarestorage.com" || buckets[0].AccessKeyID != "k-1" || buckets[0].SecretAccessKey != "s-1" || buckets[1].Binding != "MEDIA" {
		t.Errorf("decoded %+v", buckets)
	}
	if _, err := DecodeBucketsEnv(`[{"name":"x","backend":"s3","access_key_id":"${MISSING}"}]`, map[string]string{}); err == nil {
		t.Error("undefined reference accepted")
	}
}
