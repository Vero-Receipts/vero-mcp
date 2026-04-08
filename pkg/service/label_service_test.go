package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Fake LabelRepository
// ---------------------------------------------------------------------------

type fakeLabelRepo struct {
	labels map[uuid.UUID]*domain.Label
}

func newFakeLabelRepo() *fakeLabelRepo {
	return &fakeLabelRepo{labels: make(map[uuid.UUID]*domain.Label)}
}

func (r *fakeLabelRepo) Create(_ context.Context, label *domain.Label) error {
	if label.ID == uuid.Nil {
		label.ID = uuid.New()
	}
	r.labels[label.ID] = label
	return nil
}

func (r *fakeLabelRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.Label, error) {
	l, ok := r.labels[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return l, nil
}

func (r *fakeLabelRepo) FindByUserID(_ context.Context, userID uuid.UUID) ([]domain.Label, error) {
	var result []domain.Label
	for _, l := range r.labels {
		if l.UserID == userID {
			result = append(result, *l)
		}
	}
	return result, nil
}

func (r *fakeLabelRepo) Update(_ context.Context, label *domain.Label) error {
	if _, ok := r.labels[label.ID]; !ok {
		return domain.ErrNotFound
	}
	r.labels[label.ID] = label
	return nil
}

func (r *fakeLabelRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.labels[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.labels, id)
	return nil
}

// ---------------------------------------------------------------------------
// Fake LabelAssignmentRepository
// ---------------------------------------------------------------------------

type fakeLabelAssignmentRepo struct {
	assignments map[string]*domain.LabelAssignment // key: labelID+entityType+entityID
	labels      *fakeLabelRepo
}

func newFakeLabelAssignmentRepo(labels *fakeLabelRepo) *fakeLabelAssignmentRepo {
	return &fakeLabelAssignmentRepo{
		assignments: make(map[string]*domain.LabelAssignment),
		labels:      labels,
	}
}

func assignKey(labelID uuid.UUID, entityType, entityID string) string {
	return labelID.String() + ":" + entityType + ":" + entityID
}

func (r *fakeLabelAssignmentRepo) Assign(_ context.Context, a *domain.LabelAssignment) error {
	key := assignKey(a.LabelID, a.EntityType, a.EntityID)
	if _, exists := r.assignments[key]; exists {
		return domain.ErrConflict
	}
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	r.assignments[key] = a
	return nil
}

func (r *fakeLabelAssignmentRepo) Unassign(_ context.Context, labelID uuid.UUID, entityType, entityID string) error {
	key := assignKey(labelID, entityType, entityID)
	if _, exists := r.assignments[key]; !exists {
		return domain.ErrNotFound
	}
	delete(r.assignments, key)
	return nil
}

func (r *fakeLabelAssignmentRepo) FindByEntity(_ context.Context, userID uuid.UUID, entityType, entityID string) ([]domain.Label, error) {
	var result []domain.Label
	for _, a := range r.assignments {
		if a.UserID == userID && a.EntityType == entityType && a.EntityID == entityID {
			if l, ok := r.labels.labels[a.LabelID]; ok {
				result = append(result, *l)
			}
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestLabelService_CreateLabel_NameRequired(t *testing.T) {
	svc := NewLabelService(newFakeLabelRepo(), nil)
	ctx := context.Background()

	_, err := svc.CreateLabel(ctx, uuid.New(), "", "")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !errors.Is(err, domain.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}

	// Whitespace-only
	_, err = svc.CreateLabel(ctx, uuid.New(), "   ", "")
	if err == nil {
		t.Fatal("expected error for whitespace-only name")
	}
}

func TestLabelService_CreateLabel_DefaultColor(t *testing.T) {
	svc := NewLabelService(newFakeLabelRepo(), nil)
	ctx := context.Background()

	label, err := svc.CreateLabel(ctx, uuid.New(), "MyLabel", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if label.Color != "#6B7280" {
		t.Errorf("expected default color #6B7280, got %s", label.Color)
	}
}

func TestLabelService_CreateLabel_CustomColor(t *testing.T) {
	svc := NewLabelService(newFakeLabelRepo(), nil)
	ctx := context.Background()

	label, err := svc.CreateLabel(ctx, uuid.New(), "MyLabel", "#FF0000")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if label.Color != "#FF0000" {
		t.Errorf("expected #FF0000, got %s", label.Color)
	}
}

func TestLabelService_UpdateLabel_ChecksOwnership(t *testing.T) {
	labelRepo := newFakeLabelRepo()
	svc := NewLabelService(labelRepo, nil)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()

	label, _ := svc.CreateLabel(ctx, ownerID, "Original", "#000")

	// Different user
	_, err := svc.UpdateLabel(ctx, otherID, label.ID, "New", "#FFF")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}

	// Owner can update
	updated, err := svc.UpdateLabel(ctx, ownerID, label.ID, "Updated", "#111")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Updated" {
		t.Errorf("expected Updated, got %s", updated.Name)
	}
}

func TestLabelService_UpdateLabel_PartialUpdate(t *testing.T) {
	labelRepo := newFakeLabelRepo()
	svc := NewLabelService(labelRepo, nil)
	ctx := context.Background()

	ownerID := uuid.New()
	label, _ := svc.CreateLabel(ctx, ownerID, "Original", "#000")

	// Update only color (empty name = keep existing)
	updated, err := svc.UpdateLabel(ctx, ownerID, label.ID, "", "#FFF")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Original" {
		t.Errorf("expected name preserved as Original, got %s", updated.Name)
	}
	if updated.Color != "#FFF" {
		t.Errorf("expected color #FFF, got %s", updated.Color)
	}
}

func TestLabelService_DeleteLabel_ChecksOwnership(t *testing.T) {
	labelRepo := newFakeLabelRepo()
	svc := NewLabelService(labelRepo, nil)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()

	label, _ := svc.CreateLabel(ctx, ownerID, "ToDelete", "#000")

	err := svc.DeleteLabel(ctx, otherID, label.ID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}

	err = svc.DeleteLabel(ctx, ownerID, label.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestLabelService_AssignLabel_ValidatesEntityType(t *testing.T) {
	labelRepo := newFakeLabelRepo()
	assignRepo := newFakeLabelAssignmentRepo(labelRepo)
	svc := NewLabelService(labelRepo, assignRepo)
	ctx := context.Background()

	ownerID := uuid.New()
	label, _ := svc.CreateLabel(ctx, ownerID, "Tag", "#000")

	_, err := svc.AssignLabel(ctx, ownerID, label.ID, "invalid_type", "entity-1")
	if !errors.Is(err, domain.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}
}

func TestLabelService_AssignLabel_ChecksOwnership(t *testing.T) {
	labelRepo := newFakeLabelRepo()
	assignRepo := newFakeLabelAssignmentRepo(labelRepo)
	svc := NewLabelService(labelRepo, assignRepo)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()

	label, _ := svc.CreateLabel(ctx, ownerID, "Tag", "#000")

	_, err := svc.AssignLabel(ctx, otherID, label.ID, "receipt", "entity-1")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestLabelService_AssignLabel_Success(t *testing.T) {
	labelRepo := newFakeLabelRepo()
	assignRepo := newFakeLabelAssignmentRepo(labelRepo)
	svc := NewLabelService(labelRepo, assignRepo)
	ctx := context.Background()

	ownerID := uuid.New()
	label, _ := svc.CreateLabel(ctx, ownerID, "Tag", "#000")

	assignment, err := svc.AssignLabel(ctx, ownerID, label.ID, "receipt", "entity-1")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if assignment.LabelID != label.ID {
		t.Errorf("expected label ID %s, got %s", label.ID, assignment.LabelID)
	}
}

func TestLabelService_UnassignLabel_ChecksOwnership(t *testing.T) {
	labelRepo := newFakeLabelRepo()
	assignRepo := newFakeLabelAssignmentRepo(labelRepo)
	svc := NewLabelService(labelRepo, assignRepo)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()

	label, _ := svc.CreateLabel(ctx, ownerID, "Tag", "#000")
	svc.AssignLabel(ctx, ownerID, label.ID, "receipt", "entity-1")

	err := svc.UnassignLabel(ctx, otherID, label.ID, "receipt", "entity-1")
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestLabelService_GetLabelsForEntity_ValidatesEntityType(t *testing.T) {
	svc := NewLabelService(newFakeLabelRepo(), newFakeLabelAssignmentRepo(newFakeLabelRepo()))
	ctx := context.Background()

	_, err := svc.GetLabelsForEntity(ctx, uuid.New(), "bad", "e-1")
	if !errors.Is(err, domain.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}
}
