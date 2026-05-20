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
	// LastRefreshedAt is when the caller most recently asked Plaid to pull
	// fresh data for this Item via /transactions/refresh. Optional — only
	// populated when a refresh-cooldown is enabled on PlaidService.
	LastRefreshedAt *time.Time `json:"last_refreshed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
