package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// Fake ReceiptItemRepository
// ---------------------------------------------------------------------------

type fakeReceiptItemRepo struct {
	items map[uuid.UUID]*domain.ReceiptItem
}

func newFakeReceiptItemRepo() *fakeReceiptItemRepo {
	return &fakeReceiptItemRepo{items: make(map[uuid.UUID]*domain.ReceiptItem)}
}

func (r *fakeReceiptItemRepo) Create(_ context.Context, item *domain.ReceiptItem) error {
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	r.items[item.ID] = item
	return nil
}

func (r *fakeReceiptItemRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.ReceiptItem, error) {
	item, ok := r.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return item, nil
}

func (r *fakeReceiptItemRepo) FindByReceiptID(_ context.Context, receiptID uuid.UUID) ([]domain.ReceiptItem, error) {
	var result []domain.ReceiptItem
	for _, item := range r.items {
		if item.ReceiptID == receiptID {
			result = append(result, *item)
		}
	}
	return result, nil
}

func (r *fakeReceiptItemRepo) Update(_ context.Context, item *domain.ReceiptItem) error {
	if _, ok := r.items[item.ID]; !ok {
		return domain.ErrNotFound
	}
	r.items[item.ID] = item
	return nil
}

func (r *fakeReceiptItemRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := r.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.items, id)
	return nil
}

func (r *fakeReceiptItemRepo) DeleteByReceiptID(_ context.Context, receiptID uuid.UUID) error {
	for id, item := range r.items {
		if item.ReceiptID == receiptID {
			delete(r.items, id)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Fake ReceiptRepository (minimal, for ownership checks)
// ---------------------------------------------------------------------------

type fakeReceiptRepo struct {
	receipts map[uuid.UUID]*domain.Receipt
}

func newFakeReceiptRepo() *fakeReceiptRepo {
	return &fakeReceiptRepo{receipts: make(map[uuid.UUID]*domain.Receipt)}
}

func (r *fakeReceiptRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.Receipt, error) {
	rcpt, ok := r.receipts[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return rcpt, nil
}

// Stub methods to satisfy the interface.
func (r *fakeReceiptRepo) Create(_ context.Context, rcpt *domain.Receipt) error {
	if rcpt.ID == uuid.Nil {
		rcpt.ID = uuid.New()
	}
	r.receipts[rcpt.ID] = rcpt
	return nil
}
func (r *fakeReceiptRepo) FindByIDWithMatch(context.Context, uuid.UUID) (*domain.ReceiptWithMatch, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeReceiptRepo) FindByUserID(context.Context, uuid.UUID, domain.ReceiptFilter) ([]domain.Receipt, error) {
	return nil, nil
}
func (r *fakeReceiptRepo) FindByUserIDWithMatches(context.Context, uuid.UUID, domain.ReceiptFilter) ([]domain.ReceiptWithMatch, int, error) {
	return nil, 0, nil
}
func (r *fakeReceiptRepo) FindUnmatchedValid(context.Context, uuid.UUID) ([]domain.Receipt, error) {
	return nil, nil
}
func (r *fakeReceiptRepo) Update(context.Context, *domain.Receipt) error     { return nil }
func (r *fakeReceiptRepo) UpdateStatus(context.Context, uuid.UUID, string) error { return nil }
func (r *fakeReceiptRepo) UpdateThumbnailURL(context.Context, uuid.UUID, string) error {
	return nil
}
func (r *fakeReceiptRepo) FindWithoutThumbnails(context.Context, int, int) ([]domain.Receipt, error) {
	return nil, nil
}
func (r *fakeReceiptRepo) Delete(context.Context, uuid.UUID) error { return nil }
func (r *fakeReceiptRepo) CountUnmatched(context.Context, uuid.UUID) (int, error) {
	return 0, nil
}
func (r *fakeReceiptRepo) FindHardDuplicate(_ context.Context, _ uuid.UUID, _ repository.DedupKey) (*domain.Receipt, error) {
	return nil, domain.ErrNotFound
}
func (r *fakeReceiptRepo) FindSoftDuplicate(_ context.Context, _ uuid.UUID, _ string, _ float64, _ time.Time, _ int) (*domain.Receipt, error) {
	return nil, domain.ErrNotFound
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestReceiptItemService_ListByReceipt_OwnershipCheck(t *testing.T) {
	receiptRepo := newFakeReceiptRepo()
	itemRepo := newFakeReceiptItemRepo()
	svc := NewReceiptItemService(itemRepo, receiptRepo)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()

	receipt := &domain.Receipt{UserID: ownerID, Status: "unmatched"}
	receiptRepo.Create(ctx, receipt)

	// Other user cannot list items
	_, err := svc.ListByReceipt(ctx, otherID, receipt.ID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}

	// Owner can list
	items, err := svc.ListByReceipt(ctx, ownerID, receipt.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestReceiptItemService_Create_SetsReceiptAndUserID(t *testing.T) {
	receiptRepo := newFakeReceiptRepo()
	itemRepo := newFakeReceiptItemRepo()
	svc := NewReceiptItemService(itemRepo, receiptRepo)
	ctx := context.Background()

	ownerID := uuid.New()
	receipt := &domain.Receipt{UserID: ownerID, Status: "unmatched"}
	receiptRepo.Create(ctx, receipt)

	item := &domain.ReceiptItem{
		Description: "Latte",
		Quantity:    1,
		UnitPrice:   5.50,
		Price:       5.50,
	}

	if err := svc.Create(ctx, ownerID, receipt.ID, item); err != nil {
		t.Fatalf("create: %v", err)
	}

	if item.ReceiptID != receipt.ID {
		t.Errorf("expected receipt_id %s, got %s", receipt.ID, item.ReceiptID)
	}
	if item.UserID != ownerID {
		t.Errorf("expected user_id %s, got %s", ownerID, item.UserID)
	}
}

func TestReceiptItemService_Create_OwnershipCheck(t *testing.T) {
	receiptRepo := newFakeReceiptRepo()
	itemRepo := newFakeReceiptItemRepo()
	svc := NewReceiptItemService(itemRepo, receiptRepo)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()
	receipt := &domain.Receipt{UserID: ownerID, Status: "unmatched"}
	receiptRepo.Create(ctx, receipt)

	item := &domain.ReceiptItem{Description: "Item", Quantity: 1, Price: 1}
	err := svc.Create(ctx, otherID, receipt.ID, item)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}
}

func TestReceiptItemService_Delete_OwnershipCheck(t *testing.T) {
	receiptRepo := newFakeReceiptRepo()
	itemRepo := newFakeReceiptItemRepo()
	svc := NewReceiptItemService(itemRepo, receiptRepo)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()

	receipt := &domain.Receipt{UserID: ownerID, Status: "unmatched"}
	receiptRepo.Create(ctx, receipt)

	item := &domain.ReceiptItem{Description: "Item", Quantity: 1, Price: 1}
	svc.Create(ctx, ownerID, receipt.ID, item)

	// Other user cannot delete
	err := svc.Delete(ctx, otherID, item.ID)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}

	// Owner can delete
	err = svc.Delete(ctx, ownerID, item.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestReceiptItemService_Update_OwnershipCheck(t *testing.T) {
	receiptRepo := newFakeReceiptRepo()
	itemRepo := newFakeReceiptItemRepo()
	svc := NewReceiptItemService(itemRepo, receiptRepo)
	ctx := context.Background()

	ownerID := uuid.New()
	otherID := uuid.New()

	receipt := &domain.Receipt{UserID: ownerID, Status: "unmatched"}
	receiptRepo.Create(ctx, receipt)

	item := &domain.ReceiptItem{Description: "Item", Quantity: 1, Price: 1}
	svc.Create(ctx, ownerID, receipt.ID, item)

	updates := map[string]interface{}{"description": "Updated"}

	// Other user cannot update
	_, err := svc.Update(ctx, otherID, item.ID, updates)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Errorf("expected ErrForbidden, got %v", err)
	}

	// Owner can update
	updated, err := svc.Update(ctx, ownerID, item.ID, updates)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Description != "Updated" {
		t.Errorf("expected Updated, got %s", updated.Description)
	}
}

func TestReceiptItemService_ListByReceipt_NonexistentReceipt(t *testing.T) {
	receiptRepo := newFakeReceiptRepo()
	itemRepo := newFakeReceiptItemRepo()
	svc := NewReceiptItemService(itemRepo, receiptRepo)
	ctx := context.Background()

	_, err := svc.ListByReceipt(ctx, uuid.New(), uuid.New())
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}
