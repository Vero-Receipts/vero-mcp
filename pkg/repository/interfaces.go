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

type ReceiptRepository interface {
	Create(ctx context.Context, receipt *domain.Receipt) error
	FindByID(ctx context.Context, id uuid.UUID) (*domain.Receipt, error)
	FindByIDWithMatch(ctx context.Context, id uuid.UUID) (*domain.ReceiptWithMatch, error)
	FindByUserID(ctx context.Context, userID uuid.UUID, filter domain.ReceiptFilter) ([]domain.Receipt, error)
	FindByUserIDWithMatches(ctx context.Context, userID uuid.UUID, filter domain.ReceiptFilter) ([]domain.ReceiptWithMatch, error)
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

type TransactionCacheRepository interface {
	UpsertBatch(ctx context.Context, userID uuid.UUID, txs []domain.Transaction) (int, error)
	FindByUserID(ctx context.Context, userID uuid.UUID) ([]domain.Transaction, error)
	FindByUserIDWithReceipts(ctx context.Context, userID uuid.UUID, filter domain.TransactionFilter) ([]domain.TransactionWithReceipt, error)
	FindUnmatchedCandidates(ctx context.Context, userID uuid.UUID, amount float64, dateStr string) ([]domain.Transaction, error)
	FindAllUnmatched(ctx context.Context, userID uuid.UUID) ([]domain.Transaction, error)
	FindUnmatchedByDateRange(ctx context.Context, userID uuid.UUID, dateStr string) ([]domain.Transaction, error)
	FindUnmatchedTight(ctx context.Context, userID uuid.UUID, amount float64, dateStr string, isFX bool) ([]domain.Transaction, error)
	RemoveBatch(ctx context.Context, transactionIDs []string) error
	SearchUnmatched(ctx context.Context, userID uuid.UUID, search string) ([]domain.Transaction, error)
	FindByTransactionID(ctx context.Context, transactionID string) (*domain.Transaction, error)
	UpdateCorrectedCategory(ctx context.Context, transactionID string, primary, detailed string) error
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
	//
	// logoURL seeds merchants.logo_cdn_url only when no value is currently
	// set — so a Plaid URL populates new merchants, while LogoService's later
	// UpdateLogoURL replaces them with the self-hosted DO CDN URL.
	Upsert(ctx context.Context, canonicalName string, websiteDomain, logoURL, plaidEntityID *string) (*domain.Merchant, error)
	FindByID(ctx context.Context, id uuid.UUID) (*domain.Merchant, error)
	FindByNormalizedKey(ctx context.Context, key string) (*domain.Merchant, error)
	FindLogoJobCandidates(ctx context.Context, limit int) ([]MerchantLogoCandidate, error)
	UpdateLogoURL(ctx context.Context, merchantID uuid.UUID, cdnURL string) error
	UpsertLogoJob(ctx context.Context, merchantID uuid.UUID, status, source string, attempts int, lastError *string) error
}

// MerchantLogoCandidate is a merchant row that the logo resolver should try next.
// ExistingLogoURL, when set, is a Plaid-provided URL that the resolver should
// download directly and re-host rather than going through Logo.dev.
type MerchantLogoCandidate struct {
	MerchantID      uuid.UUID
	CanonicalName   string
	WebsiteDomain   *string
	ExistingLogoURL *string
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

