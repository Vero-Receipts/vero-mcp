package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

type NoteService struct {
	noteRepo repository.NoteRepository
}

func NewNoteService(noteRepo repository.NoteRepository) *NoteService {
	return &NoteService{noteRepo: noteRepo}
}

func validateEntityType(entityType string) error {
	if entityType != "receipt" && entityType != "transaction" {
		return fmt.Errorf("%w: invalid entity_type", domain.ErrBadRequest)
	}
	return nil
}

func (s *NoteService) ListByEntity(ctx context.Context, userID uuid.UUID, entityType, entityID string) ([]domain.Note, error) {
	if err := validateEntityType(entityType); err != nil {
		return nil, err
	}
	return s.noteRepo.FindByEntity(ctx, userID, entityType, entityID)
}

func (s *NoteService) Create(ctx context.Context, userID uuid.UUID, entityType, entityID, content string) (*domain.Note, error) {
	if err := validateEntityType(entityType); err != nil {
		return nil, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("%w: content is required", domain.ErrBadRequest)
	}

	note := &domain.Note{
		UserID:     userID,
		EntityType: entityType,
		EntityID:   entityID,
		Content:    content,
	}
	if err := s.noteRepo.Create(ctx, note); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *NoteService) Update(ctx context.Context, userID uuid.UUID, noteID uuid.UUID, content string) (*domain.Note, error) {
	note, err := s.noteRepo.FindByID(ctx, noteID)
	if err != nil {
		return nil, err
	}
	if note.UserID != userID {
		return nil, domain.ErrForbidden
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("%w: content is required", domain.ErrBadRequest)
	}
	note.Content = content
	if err := s.noteRepo.Update(ctx, note); err != nil {
		return nil, err
	}
	return note, nil
}

func (s *NoteService) Delete(ctx context.Context, userID uuid.UUID, noteID uuid.UUID) error {
	note, err := s.noteRepo.FindByID(ctx, noteID)
	if err != nil {
		return err
	}
	if note.UserID != userID {
		return domain.ErrForbidden
	}
	return s.noteRepo.Delete(ctx, noteID)
}
