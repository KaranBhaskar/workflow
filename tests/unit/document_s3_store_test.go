package unit_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/minio/minio-go/v7"

	"workflow/internal/documents"
)

type fakeS3Client struct {
	bucketExists   bool
	bucketChecks   int
	makeBucketCall int
	putCalls       int
	lastBucket     string
	lastObjectKey  string
	lastPayload    []byte
}

func (f *fakeS3Client) BucketExists(_ context.Context, bucketName string) (bool, error) {
	f.bucketChecks++
	f.lastBucket = bucketName
	return f.bucketExists, nil
}

func (f *fakeS3Client) MakeBucket(_ context.Context, bucketName string, _ minio.MakeBucketOptions) error {
	f.makeBucketCall++
	f.bucketExists = true
	f.lastBucket = bucketName
	return nil
}

func (f *fakeS3Client) PutObject(_ context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, _ minio.PutObjectOptions) (minio.UploadInfo, error) {
	f.putCalls++
	f.lastBucket = bucketName
	f.lastObjectKey = objectName

	payload, err := io.ReadAll(reader)
	if err != nil {
		return minio.UploadInfo{}, err
	}
	f.lastPayload = payload

	return minio.UploadInfo{
		Bucket: bucketName,
		Key:    objectName,
		Size:   objectSize,
	}, nil
}

func TestS3ObjectStoreCreatesBucketAndUploadsObject(t *testing.T) {
	t.Parallel()

	client := &fakeS3Client{}
	store := documents.NewS3ObjectStoreWithClient("workflow", client)

	stored, err := store.Put(context.Background(), "tenant-a/doc-1.txt", bytes.NewBufferString("hello object store"))
	if err != nil {
		t.Fatalf("put object: %v", err)
	}

	if client.bucketChecks != 1 {
		t.Fatalf("expected 1 bucket existence check, got %d", client.bucketChecks)
	}
	if client.makeBucketCall != 1 {
		t.Fatalf("expected 1 bucket creation, got %d", client.makeBucketCall)
	}
	if client.putCalls != 1 {
		t.Fatalf("expected 1 upload, got %d", client.putCalls)
	}
	if client.lastBucket != "workflow" {
		t.Fatalf("expected workflow bucket, got %q", client.lastBucket)
	}
	if client.lastObjectKey != "tenant-a/doc-1.txt" {
		t.Fatalf("expected tenant object key, got %q", client.lastObjectKey)
	}
	if string(client.lastPayload) != "hello object store" {
		t.Fatalf("unexpected uploaded payload %q", string(client.lastPayload))
	}
	if stored.SizeBytes != int64(len("hello object store")) {
		t.Fatalf("expected size %d, got %d", len("hello object store"), stored.SizeBytes)
	}
	if stored.Checksum == "" {
		t.Fatal("expected checksum to be populated")
	}
}

func TestS3ObjectStoreReusesBucketAcrossUploads(t *testing.T) {
	t.Parallel()

	client := &fakeS3Client{}
	store := documents.NewS3ObjectStoreWithClient("workflow", client)

	if _, err := store.Put(context.Background(), "tenant-a/one.txt", bytes.NewBufferString("one")); err != nil {
		t.Fatalf("first put object: %v", err)
	}
	if _, err := store.Put(context.Background(), "tenant-a/two.txt", bytes.NewBufferString("two")); err != nil {
		t.Fatalf("second put object: %v", err)
	}

	if client.bucketChecks != 1 {
		t.Fatalf("expected bucket existence check once, got %d", client.bucketChecks)
	}
	if client.makeBucketCall != 1 {
		t.Fatalf("expected bucket creation once, got %d", client.makeBucketCall)
	}
	if client.putCalls != 2 {
		t.Fatalf("expected 2 uploads, got %d", client.putCalls)
	}
}
