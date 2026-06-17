package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/Vero-Receipts/vero-mcp/pkg/crypto"
	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/Vero-Receipts/vero-mcp/pkg/repository"
	"github.com/google/uuid"
	plaid "github.com/plaid/plaid-go/v29/plaid"
)

type PlaidService struct {
	client        *plaid.APIClient
	itemRepo      repository.PlaidItemRepository
	userRepo      repository.UserRepository
	txCacheRepo   repository.TransactionCacheRepository
	merchantRepo  repository.MerchantRepository
	receiptSvc    *ReceiptService
	encryptionKey string // optional; empty = no encryption (plain text tokens)
	redirectURI   string
}

func NewPlaidService(
	clientID, secret, env, redirectURI, encryptionKey string,
	itemRepo repository.PlaidItemRepository,
	userRepo repository.UserRepository,
	txCacheRepo repository.TransactionCacheRepository,
	merchantRepo repository.MerchantRepository,
	receiptSvc *ReceiptService,
) *PlaidService {
	plaidEnv := plaid.Sandbox
	if env == "production" {
		plaidEnv = plaid.Production
	}

	cfg := plaid.NewConfiguration()
	cfg.AddDefaultHeader("PLAID-CLIENT-ID", clientID)
	cfg.AddDefaultHeader("PLAID-SECRET", secret)
	cfg.UseEnvironment(plaidEnv)

	return &PlaidService{
		client:        plaid.NewAPIClient(cfg),
		itemRepo:      itemRepo,
		userRepo:      userRepo,
		txCacheRepo:   txCacheRepo,
		merchantRepo:  merchantRepo,
		receiptSvc:    receiptSvc,
		encryptionKey: encryptionKey,
		redirectURI:   redirectURI,
	}
}

// resolveMerchantID upserts a merchant and returns its ID. Returns nil when
// the merchant name is empty — the transaction will land with merchant_id NULL.
// When Plaid provides a merchant_entity_id we use it as the authoritative key
// so name variations collapse onto a single merchant row.
func (s *PlaidService) resolveMerchantID(ctx context.Context, tx plaid.Transaction) *uuid.UUID {
	name := tx.GetMerchantName()
	if name == "" {
		return nil
	}

	var website *string
	if w, ok := tx.GetWebsiteOk(); ok && w != nil && *w != "" {
		website = w
	}

	var logo *string
	if l := tx.GetLogoUrl(); l != "" {
		logo = &l
	}

	var entityID *string
	if e, ok := tx.GetMerchantEntityIdOk(); ok && e != nil && *e != "" {
		entityID = e
	}

	location := extractMerchantLocation(tx)

	m, err := s.merchantRepo.Upsert(ctx, name, website, logo, entityID, location)
	if err != nil {
		slog.Error("upsert merchant", "error", err, "merchant", name)
		return nil
	}
	return &m.ID
}

// extractMerchantLocation maps the Plaid transaction's location object into a
// MerchantLocation. Returns nil when Plaid provided no location data at all,
// so merchants without location stay NULL rather than storing an empty object.
func extractMerchantLocation(tx plaid.Transaction) *domain.MerchantLocation {
	loc := tx.GetLocation()
	mloc := &domain.MerchantLocation{}
	any := false

	if v, ok := loc.GetAddressOk(); ok && v != nil && *v != "" {
		mloc.Address = v
		any = true
	}
	if v, ok := loc.GetCityOk(); ok && v != nil && *v != "" {
		mloc.City = v
		any = true
	}
	if v, ok := loc.GetRegionOk(); ok && v != nil && *v != "" {
		mloc.Region = v
		any = true
	}
	if v, ok := loc.GetPostalCodeOk(); ok && v != nil && *v != "" {
		mloc.PostalCode = v
		any = true
	}
	if v, ok := loc.GetCountryOk(); ok && v != nil && *v != "" {
		mloc.Country = v
		any = true
	}
	if v, ok := loc.GetLatOk(); ok && v != nil {
		mloc.Lat = v
		any = true
	}
	if v, ok := loc.GetLonOk(); ok && v != nil {
		mloc.Lon = v
		any = true
	}
	if v, ok := loc.GetStoreNumberOk(); ok && v != nil && *v != "" {
		mloc.StoreNumber = v
		any = true
	}

	if !any {
		return nil
	}
	return mloc
}

func (s *PlaidService) CreateLinkToken(ctx context.Context, userID uuid.UUID) (interface{}, error) {
	req := plaid.NewLinkTokenCreateRequest(
		"Vero Receipts",
		"en",
		[]plaid.CountryCode{plaid.COUNTRYCODE_US},
		plaid.LinkTokenCreateRequestUser{ClientUserId: userID.String()},
	)
	req.SetProducts([]plaid.Products{plaid.PRODUCTS_TRANSACTIONS})
	if s.redirectURI != "" {
		req.SetRedirectUri(s.redirectURI)
	}

	slog.Info("plaid: create_link_token request",
		"user_id", userID.String(),
		"products", "transactions",
		"redirect_uri", s.redirectURI,
		"country_codes", "US",
	)

	resp, httpResp, err := s.client.PlaidApi.LinkTokenCreate(ctx).LinkTokenCreateRequest(*req).Execute()
	if err != nil {
		var plaidErr plaid.GenericOpenAPIError
		if errors.As(err, &plaidErr) {
			httpStatus := 0
			if httpResp != nil {
				httpStatus = httpResp.StatusCode
			}
			slog.Error("plaid: create_link_token failed",
				"user_id", userID.String(),
				"http_status", httpStatus,
				"plaid_error_body", string(plaidErr.Body()),
			)
			return nil, fmt.Errorf("create link token: %w — plaid_error_body: %s", err, string(plaidErr.Body()))
		}
		slog.Error("plaid: create_link_token failed (non-plaid error)",
			"user_id", userID.String(),
			"error", err.Error(),
		)
		return nil, fmt.Errorf("create link token: %w", err)
	}

	slog.Info("plaid: create_link_token success",
		"user_id", userID.String(),
	)
	return resp, nil
}

func (s *PlaidService) ExchangePublicToken(ctx context.Context, userID uuid.UUID, publicToken string) (string, error) {
	req := plaid.NewItemPublicTokenExchangeRequest(publicToken)
	resp, _, err := s.client.PlaidApi.ItemPublicTokenExchange(ctx).ItemPublicTokenExchangeRequest(*req).Execute()
	if err != nil {
		return "", fmt.Errorf("exchange public token: %w", err)
	}

	accessToken := resp.GetAccessToken()
	itemID := resp.GetItemId()

	// Check for duplicate Items by comparing account details
	// Get accounts for the new Item
	acctReq := plaid.NewAccountsGetRequest(accessToken)
	acctResp, _, err := s.client.PlaidApi.AccountsGet(ctx).AccountsGetRequest(*acctReq).Execute()
	if err != nil {
		return "", fmt.Errorf("get accounts for duplicate check: %w", err)
	}

	var newInstitutionID string
	if item := acctResp.GetItem(); item.HasInstitutionId() {
		newInstitutionID = item.GetInstitutionId()
	}
	newAccounts := acctResp.GetAccounts()

	// Check existing Items for this user
	existingItems, err := s.itemRepo.FindByUserID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("find existing items: %w", err)
	}

	for _, existingItem := range existingItems {
		existingAccessToken, err := crypto.DecryptToken(existingItem.AccessToken, s.encryptionKey)
		if err != nil {
			continue
		}

		existingAcctReq := plaid.NewAccountsGetRequest(existingAccessToken)
		existingAcctResp, _, err := s.client.PlaidApi.AccountsGet(ctx).AccountsGetRequest(*existingAcctReq).Execute()
		if err != nil {
			continue
		}

		var existingInstitutionID string
		if item := existingAcctResp.GetItem(); item.HasInstitutionId() {
			existingInstitutionID = item.GetInstitutionId()
		}
		existingAccounts := existingAcctResp.GetAccounts()

		// Check if same institution
		if newInstitutionID != existingInstitutionID {
			continue
		}

		// Check for matching accounts (same institution, name, and mask)
		for _, newAcct := range newAccounts {
			for _, existingAcct := range existingAccounts {
				if newAcct.GetName() == existingAcct.GetName() &&
					newAcct.GetMask() == existingAcct.GetMask() {
					slog.Warn("duplicate item detected",
						"user_id", userID,
						"institution_id", newInstitutionID,
					)
					return "", fmt.Errorf("account already linked at this institution")
				}
			}
		}
	}

	encrypted, err := crypto.EncryptToken(accessToken, s.encryptionKey)
	if err != nil {
		return "", fmt.Errorf("encrypt access token: %w", err)
	}

	item := &domain.PlaidItem{
		UserID:      userID,
		ItemID:      itemID,
		AccessToken: encrypted,
	}
	if err := s.itemRepo.Create(ctx, item); err != nil {
		return "", fmt.Errorf("store plaid item: %w", err)
	}

	// Mark the user as bank-connected in the users table.
	if err := s.userRepo.SetBankConnected(ctx, userID, true); err != nil {
		slog.Error("set bank connected flag", "error", err, "user_id", userID)
		// Non-fatal: the item was created, the flag is a denormalized convenience
	}

	// Kick off an initial transaction sync in the background so the cache is
	// populated immediately rather than waiting for a webhook to arrive.
	go func() {
		if _, err := s.ForceSyncForWebhook(context.Background(), itemID); err != nil {
			slog.Error("initial sync after token exchange", "error", err, "item_id", itemID)
		}
	}()

	return itemID, nil
}

func (s *PlaidService) GetAccounts(ctx context.Context, userID uuid.UUID) ([]map[string]interface{}, error) {
	items, err := s.itemRepo.FindByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return []map[string]interface{}{}, nil
	}

	seen := make(map[string]bool)
	var allAccounts []map[string]interface{}
	for _, item := range items {
		accessToken, err := crypto.DecryptToken(item.AccessToken, s.encryptionKey)
		if err != nil {
			slog.Error("decrypt access token", "error", err, "item_id", item.ItemID)
			continue
		}

		req := plaid.NewAccountsGetRequest(accessToken)
		resp, _, err := s.client.PlaidApi.AccountsGet(ctx).AccountsGetRequest(*req).Execute()
		if err != nil {
			slog.Error("fetch accounts", "error", err, "item_id", item.ItemID)
			continue
		}

		institutionName := "Unknown Institution"
		respItem := resp.GetItem()
		if respItem.HasInstitutionId() {
			instID := respItem.GetInstitutionId()
			if instID != "" {
				instReq := plaid.NewInstitutionsGetByIdRequest(instID, []plaid.CountryCode{plaid.COUNTRYCODE_US})
				instResp, _, instErr := s.client.PlaidApi.InstitutionsGetById(ctx).InstitutionsGetByIdRequest(*instReq).Execute()
				if instErr == nil {
					institutionName = instResp.GetInstitution().Name
				}
			}
		}

		for _, acct := range resp.GetAccounts() {
			accountID := acct.GetAccountId()
			// Deduplicate — skip if we already added this account ID
			if seen[accountID] {
				continue
			}
			seen[accountID] = true
			allAccounts = append(allAccounts, map[string]interface{}{
				"id":               accountID,
				"item_id":          item.ItemID,
				"name":             acct.GetName(),
				"mask":             acct.GetMask(),
				"type":             acct.GetType(),
				"subtype":          acct.GetSubtype(),
				"institution_name": institutionName,
			})
		}
	}
	return allAccounts, nil
}

// DeleteAccount removes the Plaid item associated with the given account ID
// by fetching the account from Plaid, finding its item, and deleting the item.
func (s *PlaidService) DeleteAccount(ctx context.Context, userID uuid.UUID, accountID string) error {
	items, err := s.itemRepo.FindByUserID(ctx, userID)
	if err != nil {
		return err
	}

	for _, item := range items {
		accessToken, err := crypto.DecryptToken(item.AccessToken, s.encryptionKey)
		if err != nil {
			slog.Error("decrypt access token", "error", err, "item_id", item.ItemID)
			continue
		}

		req := plaid.NewAccountsGetRequest(accessToken)
		resp, _, err := s.client.PlaidApi.AccountsGet(ctx).AccountsGetRequest(*req).Execute()
		if err != nil {
			continue
		}

		for _, acct := range resp.GetAccounts() {
			if acct.GetAccountId() == accountID {
				// Found the item containing this account — remove the item
				if err := s.itemRepo.Delete(ctx, item.ID); err != nil {
					return err
				}
				// Check if the user has any remaining bank connections
				remaining, rErr := s.itemRepo.FindByUserID(ctx, userID)
				if rErr == nil && len(remaining) == 0 {
					if fErr := s.userRepo.SetBankConnected(ctx, userID, false); fErr != nil {
						slog.Error("clear bank connected flag", "error", fErr, "user_id", userID)
					}
				}
				return nil
			}
		}
	}
	return domain.ErrNotFound
}

type SyncResult struct {
	Transactions []domain.TransactionResponse `json:"transactions"`
	HasMore      bool                         `json:"hasMore"`
	Cursor       string                       `json:"cursor"`
	// Pagination metadata, set by ListTransactions when a page is requested.
	// Total is the number of transactions matching the filter across all pages.
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	// TotalSpent is the sum of expense (positive) amounts across every
	// transaction matching the filter — independent of the current page.
	TotalSpent float64 `json:"total_spent"`
}

func (s *PlaidService) SyncTransactions(ctx context.Context, userID uuid.UUID, cursor string) (*SyncResult, error) {
	items, err := s.itemRepo.FindByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return &SyncResult{Transactions: []domain.TransactionResponse{}}, nil
	}

	txMap := make(map[string]domain.TransactionResponse)
	var nextCursor string
	hasMore := false
	var cacheTxs []domain.Transaction

	for _, item := range items {
		accessToken, err := crypto.DecryptToken(item.AccessToken, s.encryptionKey)
		if err != nil {
			slog.Error("decrypt access token", "error", err, "item_id", item.ItemID)
			continue
		}

		syncCursor := cursor
		if syncCursor == "" {
			syncCursor = item.SyncCursor
		}

		// Paginate through all available transaction pages from Plaid.
		for {
			req := plaid.NewTransactionsSyncRequest(accessToken)
			if syncCursor != "" {
				req.SetCursor(syncCursor)
			}
			req.SetCount(500)

			resp, _, err := s.client.PlaidApi.TransactionsSync(ctx).TransactionsSyncRequest(*req).Execute()
			if err != nil {
				slog.Error("sync transactions", "error", err, "item_id", item.ItemID)
				break
			}

			slog.Info("plaid sync response",
				"item_id", item.ItemID,
				"added", len(resp.GetAdded()),
				"modified", len(resp.GetModified()),
				"removed", len(resp.GetRemoved()),
				"has_more", resp.GetHasMore(),
			)

			for _, tx := range append(resp.GetAdded(), resp.GetModified()...) {
				var merchantName *string
				if mn := tx.GetMerchantName(); mn != "" {
					merchantName = &mn
				}
				var merchantLogo *string
				if logo := tx.GetLogoUrl(); logo != "" {
					merchantLogo = &logo
				}
				merchantID := s.resolveMerchantID(ctx, tx)
				categoryJSON, _ := json.Marshal(tx.GetCategory())

				slog.Debug("plaid tx raw fields",
					"tx_id", tx.GetTransactionId(),
					"date", tx.GetDate(),
					"pending", tx.GetPending(),
					"payment_channel", tx.GetPaymentChannel(),
					"logo_url", tx.GetLogoUrl(),
					"website", tx.GetWebsite(),
				)

				// Extract datetime from Plaid response
				// GetDatetimeOk() returns (*time.Time, bool)
				var datetime *time.Time

				// Log what Plaid is sending for the first few transactions
				dtVal, dtOk := tx.GetDatetimeOk()
				authDtVal, authDtOk := tx.GetAuthorizedDatetimeOk()
				slog.Debug("plaid transaction datetime fields",
					"tx_id", tx.GetTransactionId(),
					"date", tx.GetDate(),
					"datetime_ok", dtOk,
					"auth_datetime_ok", authDtOk,
				)

				// Option 1: Try Datetime field
				if dtOk {
					if dtVal != nil && !dtVal.IsZero() {
						datetime = dtVal
						slog.Debug("extracted datetime",
							"tx_id", tx.GetTransactionId(),
							"datetime", dtVal.Format(time.RFC3339),
							"source", "Datetime",
						)
					} else {
						slog.Debug("datetime field set but empty",
							"tx_id", tx.GetTransactionId(),
						)
					}
				} else {
					slog.Debug("datetime field not available",
						"tx_id", tx.GetTransactionId(),
					)
				}

				// Option 2: Try AuthorizedDatetime as fallback
				if datetime == nil {
					if authDtOk {
						if authDtVal != nil && !authDtVal.IsZero() {
							datetime = authDtVal
							slog.Debug("extracted datetime from fallback",
								"tx_id", tx.GetTransactionId(),
								"datetime", authDtVal.Format(time.RFC3339),
								"source", "AuthorizedDatetime",
							)
						}
					} else {
						slog.Debug("no datetime available from any source",
							"tx_id", tx.GetTransactionId(),
						)
					}
				}

				var pfcPrimary, pfcDetailed *string
				if pfc, ok := tx.GetPersonalFinanceCategoryOk(); ok && pfc != nil {
					if v := pfc.GetPrimary(); v != "" {
						pfcPrimary = &v
					}
					if v := pfc.GetDetailed(); v != "" {
						pfcDetailed = &v
					}
				}

				var paymentChannel *string
				if ch := tx.GetPaymentChannel(); ch != "" {
					paymentChannel = &ch
				}

				txMap[tx.GetTransactionId()] = domain.TransactionResponse{
					ID:             tx.GetTransactionId(),
					AccountID:      tx.GetAccountId(),
					Amount:         tx.GetAmount(),
					Date:           tx.GetDate(),
					DateTime:       datetime,
					Name:           tx.GetName(),
					MerchantName:   merchantName,
					Category:       tx.GetCategory(),
					PFCPrimary:     pfcPrimary,
					PFCDetailed:    pfcDetailed,
					PaymentChannel: paymentChannel,
					Pending:        tx.GetPending(),
					MerchantLogo:   merchantLogo,
				}

				cacheTxs = append(cacheTxs, domain.Transaction{
					TransactionID:  tx.GetTransactionId(),
					AccountID:      tx.GetAccountId(),
					Amount:         tx.GetAmount(),
					Date:           tx.GetDate(),
					DateTime:       datetime,
					Name:           tx.GetName(),
					MerchantID:     merchantID,
					Category:       categoryJSON,
					PFCPrimary:     pfcPrimary,
					PFCDetailed:    pfcDetailed,
					PaymentChannel: paymentChannel,
					Pending:        tx.GetPending(),
				})
			}

			// Handle removed transactions
			removedIDs := make([]string, 0, len(resp.GetRemoved()))
			for _, r := range resp.GetRemoved() {
				removedIDs = append(removedIDs, r.GetTransactionId())
				delete(txMap, r.GetTransactionId())
			}
			if len(removedIDs) > 0 {
				_ = s.txCacheRepo.RemoveBatch(ctx, removedIDs)
			}

			nc := resp.GetNextCursor()
			syncCursor = nc
			nextCursor = nc

			// Persist cursor after each page so progress is saved.
			if nc != "" {
				_ = s.itemRepo.UpdateSyncCursor(ctx, item.ID, nc)
			}

			if !resp.GetHasMore() {
				break
			}
		}
	}

	// Deduplicate transactions before caching
	// When the same bank account is linked multiple times (duplicate Items)
	// Plaid returns the same real-world transaction with different transaction_ids.
	// Deduplicate by (user_id, date, amount, name) to ensure only one record per real transaction.
	if len(cacheTxs) > 0 {
		seen := make(map[string]bool)
		uniqueTxs := make([]domain.Transaction, 0, len(cacheTxs))
		duplicateCount := 0

		for _, tx := range cacheTxs {
			// Create deduplication key from real-world transaction attributes
			key := fmt.Sprintf("%s|%s|%.2f|%s",
				userID.String(), tx.Date, tx.Amount, tx.Name)

			if !seen[key] {
				seen[key] = true
				uniqueTxs = append(uniqueTxs, tx)
			} else {
				duplicateCount++
			}
		}

		if duplicateCount > 0 {
			slog.Info("deduplicated transactions",
				"original_count", len(cacheTxs),
				"unique_count", len(uniqueTxs),
				"duplicates_removed", duplicateCount,
				"user_id", userID.String(),
			)
		}

		cacheTxs = uniqueTxs
	}

	// Cache transactions
	if len(cacheTxs) > 0 {
		slog.Info("caching transactions",
			"count", len(cacheTxs),
			"user_id", userID.String(),
		)

		changed, err := s.txCacheRepo.UpsertBatch(ctx, userID, cacheTxs)
		if err != nil {
			slog.Error("cache transactions", "error", err)
		} else {
			slog.Info("transactions cached successfully",
				"count", len(cacheTxs),
				"changed", changed,
				"user_id", userID.String(),
			)

			if changed > 0 {
				// After new transactions land, try to auto-match any pending receipts.
				go s.rematchUnmatchedReceipts(context.Background(), userID)
			} else {
				slog.Debug("skipping rematch: no effective transaction changes",
					"user_id", userID.String(),
				)
			}
		}
	} else {
		slog.Info("no transactions to cache", "user_id", userID.String())
	}

	// Convert map to sorted slice
	txs := make([]domain.TransactionResponse, 0, len(txMap))
	for _, tx := range txMap {
		txs = append(txs, tx)
	}
	sort.Slice(txs, func(i, j int) bool {
		return txs[i].Date > txs[j].Date
	})

	return &SyncResult{
		Transactions: txs,
		HasMore:      hasMore,
		Cursor:       nextCursor,
	}, nil
}

// RefreshTransactions asks Plaid to immediately pull fresh data from the
// financial institution for all items belonging to the user. This is a
// fire-and-continue operation — Plaid processes the refresh asynchronously
// and will deliver updates via webhook, but we also do an immediate sync
// so any already-available changes are captured right away.
func (s *PlaidService) RefreshTransactions(ctx context.Context, userID uuid.UUID) error {
	items, err := s.itemRepo.FindByUserID(ctx, userID)
	if err != nil {
		return err
	}

	for _, item := range items {
		accessToken, err := crypto.DecryptToken(item.AccessToken, s.encryptionKey)
		if err != nil {
			slog.Error("decrypt access token for refresh", "error", err, "item_id", item.ItemID)
			continue
		}

		req := plaid.NewTransactionsRefreshRequest(accessToken)
		_, _, err = s.client.PlaidApi.TransactionsRefresh(ctx).TransactionsRefreshRequest(*req).Execute()
		if err != nil {
			// Non-fatal: Plaid may reject the refresh if one is already in progress
			slog.Warn("transactions refresh request failed", "error", err, "item_id", item.ItemID)
		} else {
			slog.Info("triggered transactions refresh", "item_id", item.ItemID)
		}
	}

	return nil
}

// ListTransactions reads from the local transaction cache (no Plaid API call)
// and enriches each transaction with its matched receipt payload.
func (s *PlaidService) ListTransactions(ctx context.Context, userID uuid.UUID, filter domain.TransactionFilter) (*SyncResult, error) {
	rows, total, totalSpent, err := s.txCacheRepo.FindByUserIDWithReceipts(ctx, userID, filter)
	if err != nil {
		return nil, err
	}

	txs := make([]domain.TransactionResponse, 0, len(rows))
	for _, row := range rows {
		var cats []string
		_ = json.Unmarshal(row.Category, &cats)

		var mName, mLogo *string
		if row.Merchant != nil {
			n := row.Merchant.CanonicalName
			mName = &n
			mLogo = row.Merchant.LogoCDNURL
		}
		resp := domain.TransactionResponse{
			ID:                   row.TransactionID,
			AccountID:            row.AccountID,
			Amount:               row.Amount,
			Date:                 row.Date,
			DateTime:             row.DateTime,
			Name:                 row.Name,
			MerchantName:         mName,
			Category:             cats,
			PFCPrimary:           row.PFCPrimary,
			PFCDetailed:          row.PFCDetailed,
			PaymentChannel:       row.PaymentChannel,
			Pending:              row.Pending,
			MerchantLogo:         mLogo,
			CorrectedPFCPrimary:  row.CorrectedPFCPrimary,
			CorrectedPFCDetailed: row.CorrectedPFCDetailed,
			Receipt:              row.Receipt,
		}
		txs = append(txs, resp)
	}

	result := &SyncResult{Transactions: txs, Total: total, TotalSpent: totalSpent}
	if filter.Limit > 0 {
		result.Limit = filter.Limit
		result.Offset = filter.Offset
		result.HasMore = filter.Offset+len(txs) < total
	}
	return result, nil
}

// rematchUnmatchedReceipts runs LLM-based matching for all unmatched receipts
// belonging to the user. Called in the background after new transactions land.
func (s *PlaidService) rematchUnmatchedReceipts(ctx context.Context, userID uuid.UUID) {
	count, err := s.receiptSvc.SuggestMatches(ctx, userID)
	if err != nil {
		slog.Error("rematch: suggest matches", "error", err, "user_id", userID)
		return
	}
	if count > 0 {
		slog.Info("rematch: matched receipts", "count", count, "user_id", userID)
	}
}

func (s *PlaidService) ForceSyncForWebhook(ctx context.Context, itemID string) ([]domain.TransactionResponse, error) {
	item, err := s.itemRepo.FindByItemID(ctx, itemID)
	if err != nil {
		return nil, err
	}

	accessToken, err := crypto.DecryptToken(item.AccessToken, s.encryptionKey)
	if err != nil {
		return nil, err
	}

	var txs []domain.TransactionResponse
	var cacheTxs []domain.Transaction
	syncCursor := item.SyncCursor

	// Paginate through all available transaction pages from Plaid.
	for {
		req := plaid.NewTransactionsSyncRequest(accessToken)
		if syncCursor != "" {
			req.SetCursor(syncCursor)
		}
		req.SetCount(500)

		resp, _, err := s.client.PlaidApi.TransactionsSync(ctx).TransactionsSyncRequest(*req).Execute()
		if err != nil {
			return nil, err
		}

		for _, tx := range append(resp.GetAdded(), resp.GetModified()...) {
			var merchantName *string
			if mn := tx.GetMerchantName(); mn != "" {
				merchantName = &mn
			}
			var merchantLogo *string
			if logo := tx.GetLogoUrl(); logo != "" {
				merchantLogo = &logo
			}
			merchantID := s.resolveMerchantID(ctx, tx)
			categoryJSON, _ := json.Marshal(tx.GetCategory())

			// Extract datetime from Plaid response
			// GetDatetimeOk() returns (*time.Time, bool)
			var datetime *time.Time

			// Option 1: Try Datetime field
			if dt, ok := tx.GetDatetimeOk(); ok {
				if dt != nil && !dt.IsZero() {
					datetime = dt
					slog.Debug("webhook: extracted datetime",
						"tx_id", tx.GetTransactionId(),
						"datetime", dt.Format(time.RFC3339),
						"source", "Datetime",
					)
				}
			}

			// Option 2: Try AuthorizedDatetime as fallback
			if datetime == nil {
				if dt, ok := tx.GetAuthorizedDatetimeOk(); ok {
					if dt != nil && !dt.IsZero() {
						datetime = dt
						slog.Debug("webhook: extracted datetime from fallback",
							"tx_id", tx.GetTransactionId(),
							"datetime", dt.Format(time.RFC3339),
							"source", "AuthorizedDatetime",
						)
					}
				}
			}

			var pfcPrimary, pfcDetailed *string
			if pfc, ok := tx.GetPersonalFinanceCategoryOk(); ok && pfc != nil {
				if v := pfc.GetPrimary(); v != "" {
					pfcPrimary = &v
				}
				if v := pfc.GetDetailed(); v != "" {
					pfcDetailed = &v
				}
			}

			var paymentChannel *string
			if ch := tx.GetPaymentChannel(); ch != "" {
				paymentChannel = &ch
			}

			txs = append(txs, domain.TransactionResponse{
				ID:             tx.GetTransactionId(),
				AccountID:      tx.GetAccountId(),
				Amount:         tx.GetAmount(),
				Date:           tx.GetDate(),
				DateTime:       datetime,
				Name:           tx.GetName(),
				MerchantName:   merchantName,
				Category:       tx.GetCategory(),
				PFCPrimary:     pfcPrimary,
				PFCDetailed:    pfcDetailed,
				PaymentChannel: paymentChannel,
				Pending:        tx.GetPending(),
				MerchantLogo:   merchantLogo,
			})

			cacheTxs = append(cacheTxs, domain.Transaction{
				TransactionID:  tx.GetTransactionId(),
				AccountID:      tx.GetAccountId(),
				Amount:         tx.GetAmount(),
				Date:           tx.GetDate(),
				DateTime:       datetime,
				Name:           tx.GetName(),
				MerchantID:     merchantID,
				Category:       categoryJSON,
				PFCPrimary:     pfcPrimary,
				PFCDetailed:    pfcDetailed,
				PaymentChannel: paymentChannel,
				Pending:        tx.GetPending(),
			})
		}

		// Remove deleted transactions from cache.
		removedIDs := make([]string, 0, len(resp.GetRemoved()))
		for _, r := range resp.GetRemoved() {
			removedIDs = append(removedIDs, r.GetTransactionId())
		}
		if len(removedIDs) > 0 {
			_ = s.txCacheRepo.RemoveBatch(ctx, removedIDs)
		}

		nc := resp.GetNextCursor()
		syncCursor = nc

		// Persist cursor after each page so progress is saved.
		if nc != "" {
			_ = s.itemRepo.UpdateSyncCursor(ctx, item.ID, nc)
		}

		if !resp.GetHasMore() {
			break
		}
	}

	// Persist new/modified transactions to cache.
	if len(cacheTxs) > 0 {
		changed, err := s.txCacheRepo.UpsertBatch(ctx, item.UserID, cacheTxs)
		if err != nil {
			slog.Error("webhook sync: cache transactions", "error", err, "item_id", itemID)
		} else if changed > 0 {
			go s.rematchUnmatchedReceipts(context.Background(), item.UserID)
		} else {
			slog.Debug("webhook sync: skipping rematch, cache unchanged",
				"user_id", item.UserID,
				"item_id", itemID,
			)
		}
	}

	return txs, nil
}

func (s *PlaidService) FindUserIDByItemID(ctx context.Context, itemID string) (uuid.UUID, error) {
	item, err := s.itemRepo.FindByItemID(ctx, itemID)
	if err != nil {
		return uuid.Nil, err
	}
	return item.UserID, nil
}
