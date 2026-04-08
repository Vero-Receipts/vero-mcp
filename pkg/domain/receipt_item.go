package domain

import (
	"time"

	"github.com/google/uuid"
)

type ReceiptItem struct {
	ID          uuid.UUID `json:"id"`
	ReceiptID   uuid.UUID `json:"receipt_id"`
	UserID      uuid.UUID `json:"user_id"`
	Description string    `json:"description"`
	Quantity    float64   `json:"quantity"`
	UnitPrice   float64   `json:"unit_price"`
	Price       float64   `json:"price"`
	SortOrder   int       `json:"sort_order"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
