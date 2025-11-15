package documents

import (
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

const StatusUploaded Status = "uploaded"

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
	Create(ctx context.Context, document Document) error
	GetByID(ctx context.Context, tenantID, documentID string) (Document, error)
}

type ObjectStore interface {
	Put(ctx context.Context, objectKey string, content io.Reader) (StoredObject, error)
}

type MemoryRepository struct {
	mu        sync.RWMutex
	documents map[string]Document
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

func (r *MemoryRepository) Create(_ context.Context, document Document) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.documents[document.ID] = document
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
	stored, err := s.objectStore.Put(ctx, objectKey, params.Content)
	if err != nil {
		return Document{}, fmt.Errorf("store document object: %w", err)
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
		CreatedAt:   s.now(),
	}

	if err := s.repository.Create(ctx, document); err != nil {
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
