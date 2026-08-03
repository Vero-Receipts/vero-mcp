package repository

import (
	"context"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
)

type UserRepository interface {
	FindByID(ctx context.Context, id uuid.UUID) (*domain.User, error)
	Upsert(ctx context.Context, user *domain.User) error
	SetBankConnected(ctx context.Context, userID uuid.UUID, connected bool) error
}

type PlaidItemRepository interface {
	Create(ctx context.Context, item *domain.PlaidItem) error
	FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.PlaidItem, error)
	FindByItemID(ctx context.Context, itemID string) (*domain.PlaidItem, error)
	UpdateSyncCursor(ctx context.Context, id uuid.UUID, cursor string) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// PlaidAccountRepository persists accounts (with card mask) fetched from
// /accounts/get, keyed by Plaid account_id.
type PlaidAccountRepository interface {
	Upsert(ctx context.Context, a *domain.PlaidAccount) error
	FindByAccountID(ctx context.Context, accountID string) (*domain.PlaidAccount, error)
	FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.PlaidAccount, error)
}

type ReceiptRepository interface {
	Create(ctx context.Context, receipt *domain.Receipt) error
	FindByID(ctx context.Context, id uuid.UUID) (*domain.Receipt, error)
	FindByIDWithMatch(ctx context.Context, id uuid.UUID) (*domain.ReceiptWithMatch, error)
	FindByUserID(ctx context.Context, userID uuid.UUID, filter domain.ReceiptFilter) ([]domain.Receipt, error)
	FindByUserIDWithMatches(ctx context.Context, userID uuid.UUID, filter domain.ReceiptFilter) ([]domain.ReceiptWithMatch, int, error)
	FindUnmatchedValid(ctx context.Context, userID uuid.UUID) ([]domain.Receipt, error)
	Update(ctx context.Context, receipt *domain.Receipt) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) error
	UpdateThumbnailURL(ctx context.Context, id uuid.UUID, thumbnailURL string) error
	FindWithoutThumbnails(ctx context.Context, offset, limit int) ([]domain.Receipt, error)
	Delete(ctx context.Context, id uuid.UUID) error
	CountUnmatched(ctx context.Context, userID uuid.UUID) (int, error)
	FindHardDuplicate(ctx context.Context, userID uuid.UUID, key DedupKey) (*domain.Receipt, error)
	FindSoftDuplicate(ctx context.Context, userID uuid.UUID, merchantKey string, total float64, date time.Time, dateWindowDays int) (*domain.Receipt, error)
}

type ReceiptMatchRepository interface {
	Create(ctx context.Context, match *domain.ReceiptMatch) error
	FindByReceiptID(ctx context.Context, receiptID uuid.UUID) (*domain.ReceiptMatch, error)
	FindByTransactionID(ctx context.Context, txID string) (*domain.ReceiptMatch, error)
	UpdateMethod(ctx context.Context, id uuid.UUID, method string) error
	DeleteByReceiptID(ctx context.Context, receiptID uuid.UUID) error
}

// ReceiptMatchSuggestionRepository stores proposed receipt↔transaction links
// awaiting a user decision. Unlike ReceiptMatchRepository these are
// non-exclusive, and a rejected pair is retained rather than deleted so the
// matcher never proposes it again.
type ReceiptMatchSuggestionRepository interface {
	ReplaceForReceipt(ctx context.Context, receiptID uuid.UUID, suggestions []domain.ReceiptMatchSuggestion) error
	FindByReceiptID(ctx context.Context, receiptID uuid.UUID) ([]domain.ReceiptMatchSuggestion, error)
	FindByTransactionID(ctx context.Context, txID string) ([]domain.ReceiptMatchSuggestion, error)
	FindPair(ctx context.Context, receiptID uuid.UUID, txID string) (*domain.ReceiptMatchSuggestion, error)
	MarkRejected(ctx context.Context, receiptID uuid.UUID, txID string) error
	DeleteForReceipt(ctx context.Context, receiptID uuid.UUID) error
	DeleteForTransaction(ctx context.Context, txID string) error
	CountPendingByUser(ctx context.Context, userID uuid.UUID) (int, error)
}

type TransactionCacheRepository interface {
	UpsertBatch(ctx context.Context, userID uuid.UUID, txs []domain.Transaction) (int, error)
	FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Transaction, error)
	FindByUserIDWithReceipts(ctx context.Context, userID uuid.UUID, filter domain.TransactionFilter) ([]domain.TransactionWithReceipt, int, float64, error)
	FindUnmatchedCandidates(ctx context.Context, userID uuid.UUID, amount float64, dateStr string) ([]domain.Transaction, error)
	FindAllUnmatched(ctx context.Context, userID uuid.UUID) ([]domain.Transaction, error)
	FindUnmatchedByDateRange(ctx context.Context, userID uuid.UUID, dateStr string) ([]domain.Transaction, error)
	FindUnmatchedTight(ctx context.Context, userID, receiptID uuid.UUID, amount float64, dateStr string, isFX bool) ([]domain.Transaction, error)
	FindUnmatchedByDateOnly(ctx context.Context, userID, receiptID uuid.UUID, dateStr string) ([]domain.Transaction, error)
	FindUnmatchedByAmountOnly(ctx context.Context, userID, receiptID uuid.UUID, amount float64, ingestedAt time.Time, isFX bool) ([]domain.Transaction, error)
	RemoveBatch(ctx context.Context, transactionIDs []string) error
	SearchUnmatched(ctx context.Context, userID uuid.UUID, search string) ([]domain.Transaction, error)
	FindByTransactionID(ctx context.Context, transactionID string) (*domain.Transaction, error)
	UpdateCorrectedCategory(ctx context.Context, transactionID string, primary, detailed string) error
	FindRecurringCandidates(ctx context.Context, userID uuid.UUID) ([]domain.RecurringCandidate, error)
	SetRecurring(ctx context.Context, transactionIDs []string) error
}

type MerchantAliasRepository interface {
	FindCanonical(ctx context.Context, alias string) (string, error)
	AreSameMerchant(ctx context.Context, a, b string) (bool, error)
	Create(ctx context.Context, alias *domain.MerchantAlias) error
}

// MerchantRepository manages the canonical merchants table.
// Upsert is the primary write path — it preserves existing domain and
// logo_cdn_url when they're already populated, so repeated syncs don't
// clobber resolved logos with fresh Plaid URLs.
type MerchantRepository interface {
	// Upsert resolves-or-creates a merchant. When plaidEntityID is provided
	// it's the authoritative identity — matches across merchant-name
	// variations that would otherwise fork into separate rows. When absent,
	// we fall back to matching by normalized canonical name.
	Upsert(ctx context.Context, canonicalName string, websiteDomain, logoURL, plaidEntityID *string) (*domain.Merchant, error)
	FindByID(ctx context.Context, id uuid.UUID) (*domain.Merchant, error)
	FindByNormalizedKey(ctx context.Context, key string) (*domain.Merchant, error)
	UpdateLogoURL(ctx context.Context, merchantID uuid.UUID, cdnURL string) error
	UpdateDomain(ctx context.Context, merchantID uuid.UUID, domain string) error
}

type MatchAuditRepository interface {
	Create(ctx context.Context, entry *domain.MatchAuditEntry) error
}

type CategoryCorrectionCacheRepository interface {
	FindByMerchantAndCategory(ctx context.Context, merchantCanonical, pfcDetailed string) (*domain.CategoryCorrectionCache, error)
	Create(ctx context.Context, entry *domain.CategoryCorrectionCache) error
}

type ReceiptItemRepository interface {
	FindByReceiptID(ctx context.Context, receiptID uuid.UUID) ([]domain.ReceiptItem, error)
	FindByID(ctx context.Context, id uuid.UUID) (*domain.ReceiptItem, error)
	Create(ctx context.Context, item *domain.ReceiptItem) error
	Update(ctx context.Context, item *domain.ReceiptItem) error
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteByReceiptID(ctx context.Context, receiptID uuid.UUID) error
}

type NoteRepository interface {
	Create(ctx context.Context, note *domain.Note) error
	FindByID(ctx context.Context, id uuid.UUID) (*domain.Note, error)
	FindByEntity(ctx context.Context, userID uuid.UUID, entityType, entityID string) ([]domain.Note, error)
	Update(ctx context.Context, note *domain.Note) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type LabelRepository interface {
	Create(ctx context.Context, label *domain.Label) error
	FindByID(ctx context.Context, id uuid.UUID) (*domain.Label, error)
	FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Label, error)
	Update(ctx context.Context, label *domain.Label) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type LabelAssignmentRepository interface {
	Assign(ctx context.Context, assignment *domain.LabelAssignment) error
	Unassign(ctx context.Context, labelID uuid.UUID, entityType, entityID string) error
	FindByEntity(ctx context.Context, userID uuid.UUID, entityType, entityID string) ([]domain.Label, error)
}
