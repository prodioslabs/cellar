package blob

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type fakeS3 struct {
	objects map[string][]byte
}

func (f *fakeS3) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	b, err := io.ReadAll(params.Body)
	if err != nil {
		return nil, err
	}
	if f.objects == nil {
		f.objects = map[string][]byte{}
	}
	f.objects[*params.Key] = b
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeS3) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	b, ok := f.objects[*params.Key]
	if !ok {
		return nil, io.EOF
	}
	return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(b))}, nil
}

func (f *fakeS3) DeleteObject(_ context.Context, params *s3.DeleteObjectInput, _ ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	delete(f.objects, *params.Key)
	return &s3.DeleteObjectOutput{}, nil
}

func TestS3StoreKeyPrefix(t *testing.T) {
	f := &fakeS3{}
	s := NewS3WithClient(f, "bucket", "pref")
	if s.Backend() != "s3" {
		t.Fatalf("backend=%s", s.Backend())
	}
	if err := s.Put(context.Background(), "snapshots/demo/snapshot_1.tar", bytes.NewReader([]byte("tar"))); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.objects["pref/snapshots/demo/snapshot_1.tar"]; !ok {
		t.Fatalf("keys=%v", f.objects)
	}
	r, err := s.Get(context.Background(), "snapshots/demo/snapshot_1.tar")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if string(b) != "tar" {
		t.Fatalf("got %q", b)
	}
	if err := s.Delete(context.Background(), "snapshots/demo/snapshot_1.tar"); err != nil {
		t.Fatal(err)
	}
}
