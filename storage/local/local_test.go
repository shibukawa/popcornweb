package local

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/shibukawa/popcornweb/storage"
)

func TestLocalBucketRoundTrip(t *testing.T) {
	bucket, err := New("uploads", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := bucket.Put(ctx, "uploads/a.txt", strings.NewReader("hello"), storage.PutOptions{ContentType: "text/plain", Metadata: map[string]string{"owner": "u1"}}); err != nil {
		t.Fatal(err)
	}
	object, err := bucket.Get(ctx, "uploads/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(object.Body)
	object.Body.Close()
	if string(body) != "hello" || object.ContentType != "text/plain" || object.Size != 5 || object.ETag == "" || object.Metadata["owner"] != "u1" {
		t.Errorf("object %+v body %q", object.ObjectInfo, body)
	}
	info, err := bucket.Head(ctx, "uploads/a.txt")
	if err != nil || info.ETag != object.ETag {
		t.Errorf("head %+v %v", info, err)
	}
	if _, err := bucket.Get(ctx, "uploads/missing.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("missing key: %v", err)
	}
	if _, err := bucket.Head(ctx, "../escape"); err == nil {
		t.Error("an escaping key was accepted")
	}
	for _, key := range []string{"uploads/b.txt", "uploads/c.txt", "other/d.txt"} {
		if err := bucket.Put(ctx, key, strings.NewReader(key), storage.PutOptions{ContentType: "text/plain"}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := bucket.List(ctx, storage.ListOptions{Prefix: "uploads/", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 2 || page.Objects[0].Key != "uploads/a.txt" || page.NextCursor != "uploads/b.txt" {
		t.Errorf("first page %+v", page)
	}
	page, err = bucket.List(ctx, storage.ListOptions{Prefix: "uploads/", Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(page.Objects) != 1 || page.Objects[0].Key != "uploads/c.txt" || page.NextCursor != "" {
		t.Errorf("second page %+v %v", page, err)
	}
	if err := bucket.Delete(ctx, "uploads/a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := bucket.Delete(ctx, "uploads/a.txt"); err != nil {
		t.Errorf("deleting a missing key: %v", err)
	}
	if _, err := bucket.Head(ctx, "uploads/a.txt"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("deleted key still present: %v", err)
	}
}
