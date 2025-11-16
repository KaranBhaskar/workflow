package documents

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound               = errors.New("document not found")
	ErrInvalidTenant          = errors.New("invalid tenant")
	ErrInvalidFilename        = errors.New("invalid filename")
	ErrMissingContent         = errors.New("missing content")
	ErrUnsupportedContentType = errors.New("unsupported content type")
)

type Status string

const (
	StatusUploaded Status = "uploaded"
	StatusIngested Status = "ingested"
)

const defaultChunkSize = 800

type Document struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SizeBytes   int64     `json:"size_bytes"`
	Checksum    string    `json:"checksum"`
	ObjectKey   string    `json:"object_key"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	IngestedAt  time.Time `json:"ingested_at,omitempty"`
	ChunkCount  int       `json:"chunk_count"`
}

type Chunk struct {
	ID            string    `json:"id"`
	DocumentID    string    `json:"document_id"`
	TenantID      string    `json:"tenant_id"`
	ChunkIndex    int       `json:"chunk_index"`
	Content       string    `json:"content"`
	TokenEstimate int       `json:"token_estimate"`
	CreatedAt     time.Time `json:"created_at"`
}

type CreateParams struct {
	Filename    string
	ContentType string
	Content     io.Reader
}

type StoredObject struct {
	SizeBytes int64
	Checksum  string
}

type Repository interface {
	Create(ctx context.Context, document Document, chunks []Chunk) error
	GetByID(ctx context.Context, tenantID, documentID string) (Document, error)
	ListChunks(ctx context.Context, tenantID, documentID string) ([]Chunk, error)
}

type ObjectStore interface {
	Put(ctx context.Context, objectKey string, content io.Reader) (StoredObject, error)
}

type MemoryRepository struct {
	mu        sync.RWMutex
	documents map[string]Document
	chunks    map[string][]Chunk
}

type Service struct {
	repository  Repository
	objectStore ObjectStore
	now         func() time.Time
	newID       func() (string, error)
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		documents: make(map[string]Document),
		chunks:    make(map[string][]Chunk),
	}
}

func NewService(repository Repository, objectStore ObjectStore) *Service {
	return &Service{
		repository:  repository,
		objectStore: objectStore,
		now:         func() time.Time { return time.Now().UTC() },
		newID:       newDocumentID,
	}
}

func (r *MemoryRepository) Create(_ context.Context, document Document, chunks []Chunk) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.documents[document.ID] = document
	r.chunks[document.ID] = append([]Chunk(nil), chunks...)
	return nil
}

func (r *MemoryRepository) GetByID(_ context.Context, tenantID, documentID string) (Document, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	document, ok := r.documents[documentID]
	if !ok || document.TenantID != tenantID {
		return Document{}, ErrNotFound
	}

	return document, nil
}

func (r *MemoryRepository) ListChunks(_ context.Context, tenantID, documentID string) ([]Chunk, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	document, ok := r.documents[documentID]
	if !ok || document.TenantID != tenantID {
		return nil, ErrNotFound
	}

	return append([]Chunk(nil), r.chunks[documentID]...), nil
}

func (s *Service) Create(ctx context.Context, tenantID string, params CreateParams) (Document, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Document{}, ErrInvalidTenant
	}

	filename := filepath.Base(strings.TrimSpace(params.Filename))
	if filename == "" || filename == "." {
		return Document{}, ErrInvalidFilename
	}

	if params.Content == nil {
		return Document{}, ErrMissingContent
	}

	contentType := normalizeContentType(params.ContentType)
	if !supportedContentType(contentType) {
		return Document{}, ErrUnsupportedContentType
	}

	documentID, err := s.newID()
	if err != nil {
		return Document{}, fmt.Errorf("generate document id: %w", err)
	}

	objectKey := path.Join(tenantID, documentID+filepath.Ext(filename))
	content, err := io.ReadAll(params.Content)
	if err != nil {
		return Document{}, fmt.Errorf("read document content: %w", err)
	}

	stored, err := s.objectStore.Put(ctx, objectKey, bytes.NewReader(content))
	if err != nil {
		return Document{}, fmt.Errorf("store document object: %w", err)
	}

	createdAt := s.now()
	chunks, err := s.buildChunks(documentID, tenantID, contentType, content, createdAt)
	if err != nil {
		return Document{}, err
	}

	document := Document{
		ID:          documentID,
		TenantID:    tenantID,
		Filename:    filename,
		ContentType: contentType,
		SizeBytes:   stored.SizeBytes,
		Checksum:    stored.Checksum,
		ObjectKey:   objectKey,
		Status:      StatusUploaded,
		CreatedAt:   createdAt,
		ChunkCount:  len(chunks),
	}
	if len(chunks) > 0 {
		document.Status = StatusIngested
		document.IngestedAt = createdAt
	}

	if err := s.repository.Create(ctx, document, chunks); err != nil {
		return Document{}, fmt.Errorf("persist document metadata: %w", err)
	}

	return document, nil
}

func (s *Service) Get(ctx context.Context, tenantID, documentID string) (Document, error) {
	if strings.TrimSpace(tenantID) == "" {
		return Document{}, ErrInvalidTenant
	}

	return s.repository.GetByID(ctx, tenantID, documentID)
}

func (s *Service) ListChunks(ctx context.Context, tenantID, documentID string) ([]Chunk, error) {
	if strings.TrimSpace(tenantID) == "" {
		return nil, ErrInvalidTenant
	}

	return s.repository.ListChunks(ctx, tenantID, documentID)
}

func newDocumentID() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(bytes), nil
}

func normalizeContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if index := strings.Index(contentType, ";"); index >= 0 {
		contentType = contentType[:index]
	}

	return strings.ToLower(contentType)
}

func supportedContentType(contentType string) bool {
	return strings.HasPrefix(contentType, "text/") || contentType == "application/pdf"
}

func (s *Service) buildChunks(documentID, tenantID, contentType string, content []byte, createdAt time.Time) ([]Chunk, error) {
	if !strings.HasPrefix(contentType, "text/") {
		return nil, nil
	}

	parts := chunkText(string(content), defaultChunkSize)
	chunks := make([]Chunk, 0, len(parts))
	for index, part := range parts {
		chunkID, err := s.newID()
		if err != nil {
			return nil, fmt.Errorf("generate chunk id: %w", err)
		}

		chunks = append(chunks, Chunk{
			ID:            chunkID,
			DocumentID:    documentID,
			TenantID:      tenantID,
			ChunkIndex:    index,
			Content:       part,
			TokenEstimate: len(strings.Fields(part)),
			CreatedAt:     createdAt,
		})
	}

	return chunks, nil
}

func chunkText(content string, maxChunkSize int) []string {
	words := strings.Fields(strings.ReplaceAll(content, "\r\n", "\n"))
	if len(words) == 0 {
		return nil
	}

	chunks := make([]string, 0, max(1, len(words)/80))
	var builder strings.Builder

	for _, word := range words {
		if builder.Len() == 0 {
			builder.WriteString(word)
			continue
		}

		if builder.Len()+1+len(word) > maxChunkSize {
			chunks = append(chunks, builder.String())
			builder.Reset()
		}

		if builder.Len() > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(word)
	}

	if builder.Len() > 0 {
		chunks = append(chunks, builder.String())
	}

	return chunks
}
