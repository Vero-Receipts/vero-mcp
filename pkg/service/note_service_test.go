package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Fake NoteRepository
// ---------------------------------------------------------------------------

type fakeNoteRepo struct {
	notes map[uuid.UUID]*domain.Note
}

func newFakeNoteRepo() *fakeNoteRepo {
	return &fakeNoteRepo{notes: make(map[uuid.UUID]*domain.Note)}
}

func (r *fakeNoteRepo) Create(_ context.Context, note *domain.Note) error {
	if note.ID == uuid.Nil {
		note.ID = uuid.New()
	}
	r.notes[note.ID] = note
	return nil
}

func (r *fakeNoteRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.Note, error) {
	n, ok := r.notes[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return n, nil
}

func (r *fakeNoteRepo) FindByEntity(_ context.Context, userID uuid.UUID, entityType, entityID string) ([]domain.Note, error) {
	var result []domain.Note
	for _, n := range r.notes {
		if n.UserID == userID && n.EntityType == entityType && n.EntityID == entityID {
			result = append(result, *n)
		}
	}
	return result, nil
}

func (r *fakeNoteRepo) Update(_ context.Context, note *domain.Note) error {
	if _, ok := r.notes[note.ID]; !ok {
		return domain.ErrNotFound
	}
	r.notes[note.ID] = note
	return nil
}

func (r *fakeNoteRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.notes[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.notes, id)
	return nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestNoteService_Create_ValidatesEntityType(t *testing.T) {
	svc := NewNoteService(newFakeNoteRepo())
	ctx := context.Background()

	_, err := svc.Create(ctx, uuid.New(), "invalid_type", "some-id", "hello")
	if err == nil {
		t.Fatal("expected error for invalid entity type")
	}
	if !errors.Is(err, domain.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}
}

func TestNoteService_Create_ValidEntityTypes(t *testing.T) {
	for _, entityType := range []string{"receipt", "transaction"} {
		t.Run(entityType, func(t *testing.T) {
			svc := NewNoteService(newFakeNoteRepo())
			ctx := context.Background()

			note, err := svc.Create(ctx, uuid.New(), entityType, "entity-123", "content")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if note.EntityType != entityType {
				t.Errorf("expected entity type %s, got %s", entityType, note.EntityType)
			}
		})
	}
}

func TestNoteService_Create_TrimsWhitespace(t *testing.T) {
	svc := NewNoteService(newFakeNoteRepo())
	ctx := context.Background()

	note, err := svc.Create(ctx, uuid.New(), "receipt", "e-1", "  trimmed content  ")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if note.Content != "trimmed content" {
		t.Errorf("expected trimmed content, got %q", note.Content)
	}
}

func TestNoteService_Create_RejectsEmptyContent(t *testing.T) {
	svc := NewNoteService(newFakeNoteRepo())
	ctx := context.Background()

	_, err := svc.Create(ctx, uuid.New(), "receipt", "e-1", "")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
	if !errors.Is(err, domain.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}

	// Whitespace-only should also be rejected
	_, err = svc.Create(ctx, uuid.New(), "receipt", "e-1", "   ")
	if err == nil {
		t.Fatal("expected error for whitespace-only content")
	}
}

func TestNoteService_Update_ChecksOwnership(t *testing.T) {
	repo := newFakeNoteRepo()
	svc := NewNoteService(repo)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()

	note, _ := svc.Create(ctx, ownerID, "receipt", "e-1", "original")

	// Different user should get ErrForbidden
	_, err := svc.Update(ctx, otherID, note.ID, "updated")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}

	// Owner can update
	updated, err := svc.Update(ctx, ownerID, note.ID, "updated content")
	if err != nil {
		t.Fatalf("update by owner: %v", err)
	}
	if updated.Content != "updated content" {
		t.Errorf("expected 'updated content', got %q", updated.Content)
	}
}

func TestNoteService_Update_RejectsEmptyContent(t *testing.T) {
	repo := newFakeNoteRepo()
	svc := NewNoteService(repo)
	ctx := context.Background()

	ownerID := uuid.New()
	note, _ := svc.Create(ctx, ownerID, "receipt", "e-1", "original")

	_, err := svc.Update(ctx, ownerID, note.ID, "   ")
	if err == nil {
		t.Fatal("expected error for empty content update")
	}
	if !errors.Is(err, domain.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}
}

func TestNoteService_Delete_ChecksOwnership(t *testing.T) {
	repo := newFakeNoteRepo()
	svc := NewNoteService(repo)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()

	note, _ := svc.Create(ctx, ownerID, "receipt", "e-1", "note to delete")

	// Different user should get ErrForbidden
	err := svc.Delete(ctx, otherID, note.ID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}

	// Owner can delete
	err = svc.Delete(ctx, ownerID, note.ID)
	if err != nil {
		t.Fatalf("delete by owner: %v", err)
	}

	// Verify deleted
	_, err = repo.FindByID(ctx, note.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Error("expected note to be deleted")
	}
}

func TestNoteService_ListByEntity_ValidatesEntityType(t *testing.T) {
	svc := NewNoteService(newFakeNoteRepo())
	ctx := context.Background()

	_, err := svc.ListByEntity(ctx, uuid.New(), "bad_type", "e-1")
	if !errors.Is(err, domain.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}
}
