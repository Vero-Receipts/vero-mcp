package service

import (
	"context"
	"fmt"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

type ReceiptItemService struct {
	itemRepo    repository.ReceiptItemRepository
	receiptRepo repository.ReceiptRepository
}

func NewReceiptItemService(itemRepo repository.ReceiptItemRepository, receiptRepo repository.ReceiptRepository) *ReceiptItemService {
	return &ReceiptItemService{itemRepo: itemRepo, receiptRepo: receiptRepo}
}

func (s *ReceiptItemService) verifyReceiptOwnership(ctx context.Context, userID, receiptID uuid.UUID) error {
	receipt, err := s.receiptRepo.FindByID(ctx, receiptID)
	if err != nil {
		return err
	}
	if receipt.UserID != userID {
		return domain.ErrForbidden
	}
	return nil
}

func (s *ReceiptItemService) ListByReceipt(ctx context.Context, userID, receiptID uuid.UUID) ([]domain.ReceiptItem, error) {
	if err := s.verifyReceiptOwnership(ctx, userID, receiptID); err != nil {
		return nil, err
	}
	return s.itemRepo.FindByReceiptID(ctx, receiptID)
}

func (s *ReceiptItemService) Create(ctx context.Context, userID, receiptID uuid.UUID, item *domain.ReceiptItem) error {
	if err := s.verifyReceiptOwnership(ctx, userID, receiptID); err != nil {
		return err
	}
	item.ReceiptID = receiptID
	item.UserID = userID
	return s.itemRepo.Create(ctx, item)
}

func (s *ReceiptItemService) Update(ctx context.Context, userID uuid.UUID, itemID uuid.UUID, updates map[string]interface{}) (*domain.ReceiptItem, error) {
	item, err := s.itemRepo.FindByID(ctx, itemID)
	if err != nil {
		return nil, err
	}
	if err := s.verifyReceiptOwnership(ctx, userID, item.ReceiptID); err != nil {
		return nil, err
	}

	if v, ok := updates["description"]; ok {
		if s, ok := v.(string); ok {
			item.Description = s
		}
	}
	if v, ok := updates["quantity"]; ok {
		if f, ok := toFloat64(v); ok {
			item.Quantity = f
		}
	}
	if v, ok := updates["unit_price"]; ok {
		if f, ok := toFloat64(v); ok {
			item.UnitPrice = f
		}
	}
	if v, ok := updates["price"]; ok {
		if f, ok := toFloat64(v); ok {
			item.Price = f
		}
	}
	if v, ok := updates["sort_order"]; ok {
		if f, ok := toFloat64(v); ok {
			item.SortOrder = int(f)
		}
	}

	if err := s.itemRepo.Update(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *ReceiptItemService) Delete(ctx context.Context, userID, itemID uuid.UUID) error {
	item, err := s.itemRepo.FindByID(ctx, itemID)
	if err != nil {
		return err
	}
	if err := s.verifyReceiptOwnership(ctx, userID, item.ReceiptID); err != nil {
		return err
	}
	return s.itemRepo.Delete(ctx, itemID)
}

func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		// Try string conversion for JSON numbers
		if s, ok := v.(string); ok {
			var f float64
			if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
				return f, true
			}
		}
		return 0, false
	}
}
