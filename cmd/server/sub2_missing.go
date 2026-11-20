package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

// Only the account-detail endpoint's structured Sub2 error is deletion
// evidence. A reverse proxy's HTML/empty 404 is an unavailable response.
type sub2NotFoundError struct{}

func (*sub2NotFoundError) Error() string { return store.Sub2MissingMessage }
func isSub2NotFound(err error) bool      { var missing *sub2NotFoundError; return errors.As(err, &missing) }
func isSub2AccountDetailPath(path string) bool {
	if !strings.HasPrefix(path, "/admin/accounts/") {
		return false
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(path, "/admin/accounts/"), 10, 64)
	return err == nil && n > 0
}
func trustedSub2NotFound(path, contentType string, body []byte) bool {
	if !isSub2AccountDetailPath(path) || !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return false
	}
	var payload struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	code := strings.TrimSpace(string(payload.Code))
	message := strings.ToLower(strings.TrimSpace(payload.Message))
	// Sub2's management error envelope uses a numeric code and a message.
	// Empty values, arbitrary error objects and reverse-proxy JSON are not proof.
	if code != "1" && code != "404" && code != `"ACCOUNT_NOT_FOUND"` {
		return false
	}
	switch message {
	case "not found", "account not found", "account does not exist", "账号不存在", "账户不存在":
		return true
	}
	return false
}

// Capture bindings before the request; a late response must never attach to
// a newer operation that happens to reuse the same remote account ID.
func (s *sub2ImportService) bindingsForRemoteID(ctx context.Context, id int64) ([]store.Sub2Import, error) {
	return s.store.ListSub2ImportsForRemoteID(ctx, s.destinationKey, id)
}

func (s *sub2ImportService) markBindingRemoteMissing(ctx context.Context, binding store.Sub2Import) error {
	if binding.DestinationKey != s.destinationKey {
		return store.ErrAccountRecoveryStale
	}
	if err := s.store.MarkSub2AccountMissing(ctx, binding); err != nil {
		return err
	}
	s.statusMu.Lock()
	delete(s.statusCache, binding.Sub2AccountID)
	s.statusMu.Unlock()
	return nil
}

func missingSub2Status(binding store.Sub2Import, checkedAt time.Time) sub2AccountStatus {
	return sub2AccountStatus{Sub2AccountID: binding.Sub2AccountID, CheckedAt: checkedAt, AssociationStatus: "missing", Error: store.Sub2MissingMessage}
}

func (s *sub2ImportService) importMissing(ctx context.Context, accountID int64) error {
	missing, err := s.store.Sub2AccountMissing(ctx, s.destinationKey, accountID)
	if err != nil {
		return err
	}
	if missing {
		return store.ErrSub2AccountMissing
	}
	return nil
}

type sub2DisplayName struct{ Name, Source string }

// attachDisplayName accepts remote names only after identity verification.
// The cache key includes the operation, so rebinding cannot inherit a name.
func (s *sub2ImportService) attachDisplayName(ctx context.Context, account store.Account, binding store.Sub2Import, snapshot *sub2AccountStatus) {
	key := fmt.Sprintf("%s/%d/%d/%s", s.destinationKey, account.ID, binding.Sub2AccountID, binding.OperationID)
	s.statusMu.Lock()
	cached := s.nameCache[key]
	if snapshot.Name != "" && !snapshot.Unknown && !snapshot.Stale {
		if s.nameCache == nil {
			s.nameCache = make(map[string]sub2DisplayName)
		}
		if len(s.nameCache) >= sub2StatusCacheLimit {
			clear(s.nameCache)
		}
		s.nameCache[key] = sub2DisplayName{snapshot.Name, "cached_remote"}
		snapshot.NameSource = "current_remote"
		s.statusMu.Unlock()
		return
	}
	s.statusMu.Unlock()
	if cached.Name != "" && (cached.Source == "cached_remote" || cached.Source == "historical_import") {
		snapshot.Name = cached.Name
		snapshot.NameSource = cached.Source
		snapshot.NameStale = true
		return
	}
	cached = sub2DisplayName{}
	if binding.ID > 0 {
		if raw, err := s.store.GetSub2ImportPayload(ctx, binding.ID); err == nil {
			var payload sub2DataImportRequest
			if json.Unmarshal(raw, &payload) == nil && len(payload.Data.Accounts) == 1 {
				cached = sub2DisplayName{payload.Data.Accounts[0].Name, "historical_import"}
			}
		}
	}
	if cached.Name == "" {
		if options, err := s.historyDeliveryOptions(ctx, account.ID); err == nil && options != nil && options.Merchant != "" {
			cached = sub2DisplayName{options.Merchant + " | " + options.Deadline + " | " + account.Email, "local_delivery"}
		}
	}
	if cached.Name == "" {
		cached = sub2DisplayName{account.Email, "email_fallback"}
	}
	snapshot.Name, snapshot.NameSource, snapshot.NameStale = cached.Name, cached.Source, true
	s.statusMu.Lock()
	if s.nameCache == nil {
		s.nameCache = make(map[string]sub2DisplayName)
	}
	if len(s.nameCache) >= sub2StatusCacheLimit {
		clear(s.nameCache)
	}
	s.nameCache[key] = cached
	s.statusMu.Unlock()
}

func (s *sub2ImportService) authorizeReimport(ctx context.Context, accountID int64, options store.DeliveryOptions, expectedVersion int64, expectedOperation string) error {
	binding, err := s.store.GetSub2Import(ctx, s.destinationKey, accountID)
	if err != nil {
		return err
	}
	if binding.State != store.Sub2RemoteMissing {
		return store.ErrSub2AccountMissing
	}
	if strings.TrimSpace(expectedOperation) == "" || binding.OperationID != strings.TrimSpace(expectedOperation) {
		return store.ErrAccountRecoveryStale
	}
	version, err := s.store.GetAccountCredentialVersion(ctx, accountID)
	if err != nil {
		return err
	}
	if expectedVersion <= 0 || version != expectedVersion {
		return store.ErrAccountRecoveryStale
	}
	payload, operation, key, err := s.buildPayload(accountID, options)
	if err != nil {
		return err
	}
	_, err = s.store.AuthorizeRemoteMissingReimport(ctx, binding, expectedVersion, operation, key, payload, options)
	return err
}
