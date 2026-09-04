package s3

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/shibukawa/popcornweb/storage"
	tinys3 "github.com/shibukawa/tinygodriver/storage/s3"
)

// Presigning makes no request, so the adapter is tested against a client
// with fixed credentials: the URL addresses the bucket at the endpoint and
// carries the SigV4 query parameters.
func TestPresignDelegatesToTheClientSigner(t *testing.T) {
	client, err := tinys3.New(tinys3.WithEndpoint("https://acct.r2.cloudflarestorage.com"), tinys3.WithRegion("auto"), tinys3.WithPathStyle(true),
		tinys3.WithCredentials(tinys3.Credentials{AccessKeyID: "AKIDEXAMPLE", SecretAccessKey: "secret"}))
	if err != nil {
		t.Fatal(err)
	}
	bucket := New(client, "uploads")
	signed, err := bucket.Presign(context.Background(), "u1/photo.png", storage.PresignOptions{Method: http.MethodPut, Expires: time.Minute, ContentType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	query := signed.Query()
	if signed.Host != "acct.r2.cloudflarestorage.com" || signed.Path != "/uploads/u1/photo.png" || query.Get("X-Amz-Signature") == "" || query.Get("X-Amz-Expires") != "60" {
		t.Errorf("presigned URL %s", signed)
	}
	if _, err := bucket.Presign(context.Background(), "k", storage.PresignOptions{Expires: 8 * 24 * time.Hour}); err == nil {
		t.Error("an expiry past the S3 limit was accepted")
	}
}
