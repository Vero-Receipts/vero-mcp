package service

import (
	"context"
	"io"
)

// FileStorage is the interface for uploading receipt images.
// Implementations: SpacesStorageService (DO Spaces), local StorageService (filesystem).
//
// UploadFile uploads with public-read ACL and returns a public URL. Used for
// non-sensitive assets (e.g. merchant logos).
//
// UploadPrivate uploads with a private ACL and does NOT return a URL — callers
// persist the object key and resolve it to a short-lived presigned URL at
// read time. Used for user content (receipts, thumbnails, email attachments).
type FileStorage interface {
	UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (string, error)
	UploadPrivate(ctx context.Context, key string, body io.Reader, contentType string) error
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
