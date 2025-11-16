package unit_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workflow/internal/documents"
)

func TestCreateStoresDocumentMetadataAndObject(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	service := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(tempDir),
	)

	document, err := service.Create(context.Background(), "tenant-a", documents.CreateParams{
		Filename:    "notes.txt",
		ContentType: "text/plain; charset=utf-8",
		Content:     bytes.NewBufferString("hello world"),
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	if document.ID == "" {
		t.Fatal("expected document id to be set")
	}
	if document.SizeBytes != 11 {
		t.Fatalf("expected 11 bytes, got %d", document.SizeBytes)
	}
	if document.Checksum == "" {
		t.Fatal("expected checksum to be populated")
	}
	if document.Status != documents.StatusIngested {
		t.Fatalf("expected ingested status, got %q", document.Status)
	}
	if document.ChunkCount == 0 {
		t.Fatal("expected chunks to be created for text upload")
	}

	if _, err := os.Stat(filepath.Join(tempDir, document.ObjectKey)); err != nil {
		t.Fatalf("expected stored object to exist: %v", err)
	}

	fetched, err := service.Get(context.Background(), "tenant-a", document.ID)
	if err != nil {
		t.Fatalf("get stored document: %v", err)
	}
	if fetched.Filename != "notes.txt" {
		t.Fatalf("expected notes.txt, got %q", fetched.Filename)
	}

	chunks, err := service.ListChunks(context.Background(), "tenant-a", document.ID)
	if err != nil {
		t.Fatalf("list stored chunks: %v", err)
	}
	if len(chunks) != document.ChunkCount {
		t.Fatalf("expected %d chunks, got %d", document.ChunkCount, len(chunks))
	}
}

func TestCreateRejectsUnsupportedContentType(t *testing.T) {
	t.Parallel()

	service := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)

	_, err := service.Create(context.Background(), "tenant-a", documents.CreateParams{
		Filename:    "image.png",
		ContentType: "image/png",
		Content:     bytes.NewBufferString("png"),
	})
	if !errors.Is(err, documents.ErrUnsupportedContentType) {
		t.Fatalf("expected unsupported content type error, got %v", err)
	}
}

func TestGetDoesNotLeakAcrossTenants(t *testing.T) {
	t.Parallel()

	service := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)

	document, err := service.Create(context.Background(), "tenant-a", documents.CreateParams{
		Filename:    "tenant-a.txt",
		ContentType: "text/plain",
		Content:     bytes.NewBufferString("tenant-a"),
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	_, err = service.Get(context.Background(), "tenant-b", document.ID)
	if !errors.Is(err, documents.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for cross-tenant lookup, got %v", err)
	}
}

func TestCreateSplitsLargeTextIntoMultipleChunks(t *testing.T) {
	t.Parallel()

	service := documents.NewService(
		documents.NewMemoryRepository(),
		documents.NewLocalObjectStore(t.TempDir()),
	)

	document, err := service.Create(context.Background(), "tenant-a", documents.CreateParams{
		Filename:    "long.txt",
		ContentType: "text/plain",
		Content:     bytes.NewBufferString(strings.Repeat("chunk words ", 120)),
	})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}

	chunks, err := service.ListChunks(context.Background(), "tenant-a", document.ID)
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if chunks[0].ChunkIndex != 0 {
		t.Fatalf("expected first chunk index 0, got %d", chunks[0].ChunkIndex)
	}
	if chunks[0].TokenEstimate == 0 {
		t.Fatal("expected token estimate to be populated")
	}
}
