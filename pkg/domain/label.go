package domain

import (
	"time"

	"github.com/google/uuid"
)

type Label struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type LabelAssignment struct {
	ID         uuid.UUID `json:"id"`
	LabelID    uuid.UUID `json:"label_id"`
	UserID     uuid.UUID `json:"user_id"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	CreatedAt  time.Time `json:"created_at"`
}
