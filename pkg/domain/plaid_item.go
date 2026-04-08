package domain

import (
	"time"

	"github.com/google/uuid"
)

type PlaidItem struct {
	ID          uuid.UUID `json:"id"`
	UserID      uuid.UUID `json:"user_id"`
	ItemID      string    `json:"item_id"`
	AccessToken string    `json:"-"` // never serialize
	SyncCursor  string    `json:"sync_cursor,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}
