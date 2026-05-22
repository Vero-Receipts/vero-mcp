package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type StorageService struct {
	baseDir string
	baseURL string // set later when webserver starts
}

func NewStorageService(baseDir string) *StorageService {
	os.MkdirAll(baseDir, 0755)
	return &StorageService{baseDir: baseDir}
}

func (s *StorageService) SetBaseURL(baseURL string) {
	s.baseURL = baseURL
}

// UploadFile implements pkg/service.FileStorage.
func (s *StorageService) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (string, error) {
	path := filepath.Join(s.baseDir, key)
	os.MkdirAll(filepath.Dir(path), 0755)

	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, body); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	url := s.baseURL + "/files/" + key
	return url, nil
}

// UploadPrivate implements pkg/service.FileStorage. The local filesystem
// implementation has no concept of ACLs, so this is just a write — callers
// still persist the object key and resolve it at read time.
func (s *StorageService) UploadPrivate(ctx context.Context, key string, body io.Reader, contentType string) error {
	path := filepath.Join(s.baseDir, key)
	os.MkdirAll(filepath.Dir(path), 0755)

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, body); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}

func (s *StorageService) BaseDir() string {
	return s.baseDir
}
