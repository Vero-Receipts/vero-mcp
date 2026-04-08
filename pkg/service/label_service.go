package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
)

type LabelService struct {
	labelRepo  repository.LabelRepository
	assignRepo repository.LabelAssignmentRepository
}

func NewLabelService(labelRepo repository.LabelRepository, assignRepo repository.LabelAssignmentRepository) *LabelService {
	return &LabelService{labelRepo: labelRepo, assignRepo: assignRepo}
}

func (s *LabelService) ListLabels(ctx context.Context, userID uuid.UUID) ([]domain.Label, error) {
	return s.labelRepo.FindByUserID(ctx, userID)
}

func (s *LabelService) CreateLabel(ctx context.Context, userID uuid.UUID, name, color string) (*domain.Label, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: name is required", domain.ErrBadRequest)
	}
	if color == "" {
		color = "#6B7280"
	}
	label := &domain.Label{
		UserID: userID,
		Name:   name,
		Color:  color,
	}
	if err := s.labelRepo.Create(ctx, label); err != nil {
		return nil, err
	}
	return label, nil
}

func (s *LabelService) UpdateLabel(ctx context.Context, userID, labelID uuid.UUID, name, color string) (*domain.Label, error) {
	label, err := s.labelRepo.FindByID(ctx, labelID)
	if err != nil {
		return nil, err
	}
	if label.UserID != userID {
		return nil, domain.ErrForbidden
	}
	name = strings.TrimSpace(name)
	if name != "" {
		label.Name = name
	}
	if color != "" {
		label.Color = color
	}
	if err := s.labelRepo.Update(ctx, label); err != nil {
		return nil, err
	}
	return label, nil
}

func (s *LabelService) DeleteLabel(ctx context.Context, userID, labelID uuid.UUID) error {
	label, err := s.labelRepo.FindByID(ctx, labelID)
	if err != nil {
		return err
	}
	if label.UserID != userID {
		return domain.ErrForbidden
	}
	return s.labelRepo.Delete(ctx, labelID)
}

func (s *LabelService) AssignLabel(ctx context.Context, userID, labelID uuid.UUID, entityType, entityID string) (*domain.LabelAssignment, error) {
	if err := validateEntityType(entityType); err != nil {
		return nil, err
	}
	label, err := s.labelRepo.FindByID(ctx, labelID)
	if err != nil {
		return nil, err
	}
	if label.UserID != userID {
		return nil, domain.ErrForbidden
	}
	assignment := &domain.LabelAssignment{
		LabelID:    labelID,
		UserID:     userID,
		EntityType: entityType,
		EntityID:   entityID,
	}
	if err := s.assignRepo.Assign(ctx, assignment); err != nil {
		return nil, err
	}
	return assignment, nil
}

func (s *LabelService) UnassignLabel(ctx context.Context, userID, labelID uuid.UUID, entityType, entityID string) error {
	label, err := s.labelRepo.FindByID(ctx, labelID)
	if err != nil {
		return err
	}
	if label.UserID != userID {
		return domain.ErrForbidden
	}
	return s.assignRepo.Unassign(ctx, labelID, entityType, entityID)
}

func (s *LabelService) GetLabelsForEntity(ctx context.Context, userID uuid.UUID, entityType, entityID string) ([]domain.Label, error) {
	if err := validateEntityType(entityType); err != nil {
		return nil, err
	}
	return s.assignRepo.FindByEntity(ctx, userID, entityType, entityID)
}
