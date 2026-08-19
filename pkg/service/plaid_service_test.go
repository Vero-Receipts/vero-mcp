package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Vero-Receipts/vero-mcp/pkg/domain"
	"github.com/google/uuid"
	plaid "github.com/plaid/plaid-go/v29/plaid"
)

type stubPlaidItemRepo struct {
	items   []domain.PlaidItem
	deleted []uuid.UUID
}

func (r *stubPlaidItemRepo) Create(context.Context, *domain.PlaidItem) error { return nil }

func (r *stubPlaidItemRepo) FindByUserID(context.Context, uuid.UUID) ([]domain.PlaidItem, error) {
	return r.items, nil
}

func (r *stubPlaidItemRepo) FindByItemID(context.Context, string) (*domain.PlaidItem, error) {
	return nil, domain.ErrNotFound
}

func (r *stubPlaidItemRepo) UpdateSyncCursor(context.Context, uuid.UUID, string) error { return nil }

func (r *stubPlaidItemRepo) Delete(_ context.Context, id uuid.UUID) error {
	r.deleted = append(r.deleted, id)
	// Mirror the delete so a follow-up FindByUserID sees the row gone.
	remaining := r.items[:0]
	for _, it := range r.items {
		if it.ID != id {
			remaining = append(remaining, it)
		}
	}
	r.items = remaining
	return nil
}

type stubPlaidUserRepo struct {
	bankConnectedCleared bool
}

func (r *stubPlaidUserRepo) FindByID(context.Context, uuid.UUID) (*domain.User, error) {
	return nil, domain.ErrNotFound
}

func (r *stubPlaidUserRepo) Upsert(context.Context, *domain.User) error { return nil }

func (r *stubPlaidUserRepo) SetBankConnected(_ context.Context, _ uuid.UUID, connected bool) error {
	if !connected {
		r.bankConnectedCleared = true
	}
	return nil
}

// plaidStub serves the two endpoints DeleteAccount touches. itemRemove decides
// how /item/remove responds; it records each call it receives.
type plaidStub struct {
	accountID  string
	itemRemove func(w http.ResponseWriter)
	calls      []string
}

func (s *plaidStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls = append(s.calls, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/accounts/get":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"accounts": []map[string]any{{
					"account_id": s.accountID,
					"name":       "Plaid Checking",
					"mask":       "0000",
					"type":       "depository",
					"subtype":    "checking",
				}},
				"item":       map[string]any{"item_id": "item-1", "institution_id": "ins_1"},
				"request_id": "req-1",
			})
		case "/item/remove":
			s.itemRemove(w)
		default:
			t.Errorf("unexpected plaid call: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func plaidErrorResponse(status int, code string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error_type":      "ITEM_ERROR",
			"error_code":      code,
			"error_message":   code,
			"display_message": nil,
			"request_id":      "req-err",
		})
	}
}

func newPlaidServiceForTest(ts *httptest.Server, itemRepo *stubPlaidItemRepo, userRepo *stubPlaidUserRepo) *PlaidService {
	cfg := plaid.NewConfiguration()
	cfg.Servers = plaid.ServerConfigurations{{URL: ts.URL}}
	return &PlaidService{
		client:   plaid.NewAPIClient(cfg),
		itemRepo: itemRepo,
		userRepo: userRepo,
		// encryptionKey empty: DecryptToken passes the token through as-is.
	}
}

// DeleteAccount must call /item/remove so Plaid stops billing the Item, and
// only then drop the local row.
func TestDeleteAccountRemovesItemAtPlaid(t *testing.T) {
	itemID := uuid.New()
	itemRepo := &stubPlaidItemRepo{items: []domain.PlaidItem{
		{ID: itemID, ItemID: "item-1", AccessToken: "access-token-1"},
	}}
	userRepo := &stubPlaidUserRepo{}
	stub := &plaidStub{
		accountID:  "acct-1",
		itemRemove: func(w http.ResponseWriter) { _, _ = w.Write([]byte(`{"request_id":"req-2"}`)) },
	}
	ts := stub.server(t)
	defer ts.Close()

	svc := newPlaidServiceForTest(ts, itemRepo, userRepo)
	if err := svc.DeleteAccount(context.Background(), uuid.New(), "acct-1"); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}

	if !contains(stub.calls, "/item/remove") {
		t.Errorf("expected /item/remove to be called, got calls: %v", stub.calls)
	}
	if len(itemRepo.deleted) != 1 || itemRepo.deleted[0] != itemID {
		t.Errorf("expected item row %s deleted, got %v", itemID, itemRepo.deleted)
	}
	if !userRepo.bankConnectedCleared {
		t.Error("expected is_bank_connected cleared once the last item was removed")
	}
}

// When /item/remove fails for a reason that leaves the Item alive, the row must
// survive: it holds the only token that can ever remove the Item, so deleting
// it would orphan a billable connection.
func TestDeleteAccountKeepsRowWhenRemoveFails(t *testing.T) {
	itemID := uuid.New()
	itemRepo := &stubPlaidItemRepo{items: []domain.PlaidItem{
		{ID: itemID, ItemID: "item-1", AccessToken: "access-token-1"},
	}}
	userRepo := &stubPlaidUserRepo{}
	stub := &plaidStub{
		accountID:  "acct-1",
		itemRemove: plaidErrorResponse(http.StatusInternalServerError, "INTERNAL_SERVER_ERROR"),
	}
	ts := stub.server(t)
	defer ts.Close()

	svc := newPlaidServiceForTest(ts, itemRepo, userRepo)
	err := svc.DeleteAccount(context.Background(), uuid.New(), "acct-1")
	if err == nil {
		t.Fatal("expected DeleteAccount to fail when /item/remove fails")
	}
	if len(itemRepo.deleted) != 0 {
		t.Errorf("expected no row deleted, got %v", itemRepo.deleted)
	}
	if userRepo.bankConnectedCleared {
		t.Error("expected is_bank_connected untouched on failure")
	}
}

// An Item that is already gone at Plaid is nothing left to bill, so the local
// row should still be dropped rather than becoming permanently undeletable.
func TestDeleteAccountDropsRowWhenItemAlreadyGone(t *testing.T) {
	for _, code := range []string{"ITEM_NOT_FOUND", "INVALID_ACCESS_TOKEN"} {
		t.Run(code, func(t *testing.T) {
			itemID := uuid.New()
			itemRepo := &stubPlaidItemRepo{items: []domain.PlaidItem{
				{ID: itemID, ItemID: "item-1", AccessToken: "access-token-1"},
			}}
			stub := &plaidStub{
				accountID:  "acct-1",
				itemRemove: plaidErrorResponse(http.StatusBadRequest, code),
			}
			ts := stub.server(t)
			defer ts.Close()

			svc := newPlaidServiceForTest(ts, itemRepo, &stubPlaidUserRepo{})
			if err := svc.DeleteAccount(context.Background(), uuid.New(), "acct-1"); err != nil {
				t.Fatalf("DeleteAccount: %v", err)
			}
			if len(itemRepo.deleted) != 1 {
				t.Errorf("expected row deleted for %s, got %v", code, itemRepo.deleted)
			}
		})
	}
}

func TestPlaidErrorCode(t *testing.T) {
	if got := plaidErrorCode(nil); got != "" {
		t.Errorf("nil error: got %q, want empty", got)
	}
	if got := plaidErrorCode(context.Canceled); got != "" {
		t.Errorf("non-plaid error: got %q, want empty", got)
	}
	err := plaid.GenericOpenAPIError{}
	if got := plaidErrorCode(err); got != "" {
		t.Errorf("empty body: got %q, want empty", got)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
