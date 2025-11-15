package documents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
)

type LocalObjectStore struct {
	baseDir string
}

func NewLocalObjectStore(baseDir string) *LocalObjectStore {
	return &LocalObjectStore{
		baseDir: baseDir,
	}
}

func (s *LocalObjectStore) Put(_ context.Context, objectKey string, content io.Reader) (StoredObject, error) {
	fullPath := filepath.Join(s.baseDir, filepath.Clean(objectKey))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return StoredObject{}, fmt.Errorf("create object directory: %w", err)
	}

	file, err := os.Create(fullPath)
	if err != nil {
		return StoredObject{}, fmt.Errorf("create object file: %w", err)
	}

	hasher := sha256.New()
	sizeBytes, copyErr := copyToFile(file, hasher, content)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(fullPath)
		return StoredObject{}, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(fullPath)
		return StoredObject{}, fmt.Errorf("close object file: %w", closeErr)
	}

	return StoredObject{
		SizeBytes: sizeBytes,
		Checksum:  hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

func copyToFile(file *os.File, hasher hash.Hash, content io.Reader) (int64, error) {
	written, err := io.Copy(io.MultiWriter(file, hasher), content)
	if err != nil {
		return 0, fmt.Errorf("write object file: %w", err)
	}

	return written, nil
}
