package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type s3Client interface {
	BucketExists(ctx context.Context, bucketName string) (bool, error)
	MakeBucket(ctx context.Context, bucketName string, options minio.MakeBucketOptions) error
	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, options minio.PutObjectOptions) (minio.UploadInfo, error)
}

type S3ObjectStore struct {
	client      s3Client
	bucket      string
	bucketMu    sync.Mutex
	bucketReady bool
}

func NewS3ObjectStore(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*S3ObjectStore, error) {
	endpoint = strings.TrimSpace(endpoint)
	accessKey = strings.TrimSpace(accessKey)
	secretKey = strings.TrimSpace(secretKey)
	bucket = strings.TrimSpace(bucket)

	switch {
	case endpoint == "":
		return nil, fmt.Errorf("s3 endpoint is required")
	case accessKey == "":
		return nil, fmt.Errorf("s3 access key is required")
	case secretKey == "":
		return nil, fmt.Errorf("s3 secret key is required")
	case bucket == "":
		return nil, fmt.Errorf("s3 bucket is required")
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("create s3 object store client: %w", err)
	}

	return NewS3ObjectStoreWithClient(bucket, client), nil
}

func NewS3ObjectStoreWithClient(bucket string, client s3Client) *S3ObjectStore {
	return &S3ObjectStore{
		client: client,
		bucket: strings.TrimSpace(bucket),
	}
}

func (s *S3ObjectStore) Put(ctx context.Context, objectKey string, content io.Reader) (StoredObject, error) {
	if err := s.ensureBucket(ctx); err != nil {
		return StoredObject{}, err
	}

	payload, err := io.ReadAll(content)
	if err != nil {
		return StoredObject{}, fmt.Errorf("read object content: %w", err)
	}

	sum := sha256.Sum256(payload)
	if _, err := s.client.PutObject(ctx, s.bucket, objectKey, bytes.NewReader(payload), int64(len(payload)), minio.PutObjectOptions{}); err != nil {
		return StoredObject{}, fmt.Errorf("put s3 object: %w", err)
	}

	return StoredObject{
		SizeBytes: int64(len(payload)),
		Checksum:  hex.EncodeToString(sum[:]),
	}, nil
}

func (s *S3ObjectStore) ensureBucket(ctx context.Context) error {
	s.bucketMu.Lock()
	defer s.bucketMu.Unlock()

	if s.bucketReady {
		return nil
	}

	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("check s3 bucket: %w", err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			exists, existsErr := s.client.BucketExists(ctx, s.bucket)
			if existsErr != nil {
				return fmt.Errorf("create s3 bucket: %w (recheck failed: %v)", err, existsErr)
			}
			if !exists {
				return fmt.Errorf("create s3 bucket: %w", err)
			}
		}
	}

	s.bucketReady = true
	return nil
}
