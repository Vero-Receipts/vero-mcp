package service

import (
	"context"
	"io"
)

// FileStorage is the interface for uploading receipt images.
// Implementations: SpacesStorageService (DO Spaces), local StorageService (filesystem).
type FileStorage interface {
	UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (string, error)
}

// ThumbnailInput describes everything needed to build a receipt thumbnail.
type ThumbnailInput struct {
	ImageBytes   []byte
	MimeType     string
	ImageURL     string
	EmailHTMLURL string
}

// ThumbnailGenerator generates receipt thumbnails.
// Implementation provided by the host application. Pass nil to disable thumbnails.
type ThumbnailGenerator interface {
	GenerateReceiptThumbnail(ctx context.Context, in ThumbnailInput) ([]byte, error)
}
