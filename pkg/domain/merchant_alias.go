package domain

import (
	"time"

	"github.com/google/uuid"
)

type MerchantAlias struct {
	ID        uuid.UUID `json:"id"`
	Canonical string    `json:"canonical"`
	Alias     string    `json:"alias"`
	Source    string    `json:"source"` // "llm", "manual", "plaid"
	CreatedAt time.Time `json:"created_at"`
}
