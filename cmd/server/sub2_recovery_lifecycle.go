package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

type lifecycleOAuthReceipt struct {
	ID                 int64     `json:"id"`
	OperationID        string    `json:"operation_id"`
	SourceAccountID    int64     `json:"source_account_id"`
	ImportOperationID  string    `json:"import_operation_id"`
	CredentialRevision int64     `json:"credential_revision"`
	Phase              string    `json:"phase"`
	AccountRevision    time.Time `json:"account_revision"`
	MayEnable          bool      `json:"may_enable"`
	Schedulable        bool      `json:"schedulable"`
}

func (s *sub2RecoveryService) processBoundLifecycleOAuth(ctx context.Context, lease *store.AccountRecoveryLease, task store.AccountRecoveryTask, delivery store.AccountDelivery, account store.Account, binding store.Sub2Import) error {
	conflict := &recoveryOperationError{Code: "checkpoint_unconfirmed", RequiresAction: true}
	if task.ResultCredentialAttemptID != delivery.CredentialVersion || task.ResultCredentialAttemptID <= 0 ||
		delivery.State == "canceled" || delivery.AllowCreate || binding.OperationID == "" {
		return conflict
	}
	path := fmt.Sprintf("/admin/accounts/%d/lifecycle-oauth", binding.Sub2AccountID)
	operation := fmt.Sprintf("auth-delivery-%d-%d", delivery.ID, delivery.CredentialVersion)
	ctx = withSub2StatusFresh(ctx)
	var receipt lifecycleOAuthReceipt
	readReceipt := func() (lifecycleOAuthReceipt, error) {
		var result lifecycleOAuthReceipt
		err := s.sub2JSONBody(ctx, http.MethodGet, path+"/"+operation, nil, &result)
		return result, err
	}
	validReceipt := func(result lifecycleOAuthReceipt) bool {
		return result.ID == binding.Sub2AccountID && result.OperationID == operation &&
			result.SourceAccountID == account.ID && result.ImportOperationID == binding.OperationID &&
			result.CredentialRevision == delivery.CredentialVersion && !result.AccountRevision.IsZero() &&
			result.Schedulable == (result.Phase == "enable")
	}
	receipt, err := readReceipt()
	if err != nil {
		var failure *recoveryOperationError
		if !errors.As(err, &failure) || failure.HTTPStatus != http.StatusNotFound {
			return err
		}
		receipt = lifecycleOAuthReceipt{}
	} else if !validReceipt(receipt) {
		return conflict
	}
	for range 5 {
		latest, err := s.store.GetSub2Import(ctx, binding.DestinationKey, account.ID)
		if err != nil {
			return err
		}
		if !delivery.MatchesOriginalBinding(latest) || s.store.ValidateAccountRecoveryVersion(ctx, task.ID) != nil {
			return conflict
		}
		detail, err := s.sub2Account(ctx, binding.Sub2AccountID)
		if err != nil {
			return err
		}
		extra, _ := detail["extra"].(map[string]any)
		_, hasMarker := extra["kkai_auth_import"]
		if verifySub2RecoveryIdentity(detail, binding.Sub2AccountID, account.ID, account, binding) != nil ||
			hasMarker && !matchesImportMarker(detail, binding) || recoveryAccountDisabled(detail) {
			return conflict
		}
		stamp, _ := detail["updated_at"].(string)
		revision, parseErr := time.Parse(time.RFC3339Nano, stamp)
		scheduled, known := boolField(detail, "schedulable")
		if parseErr != nil || revision.IsZero() || !known ||
			!receipt.AccountRevision.IsZero() && (!revision.Equal(receipt.AccountRevision) || scheduled != receipt.Schedulable) {
			return conflict
		}
		credentials, _ := detail["credentials"].(map[string]any)
		workspace, _ := credentials["chatgpt_account_id"].(string)
		if workspace == "" {
			return conflict
		}
		input := map[string]any{
			"operation_id": operation, "credential_revision": delivery.CredentialVersion,
			"expected_updated_at": revision.UTC(), "expected_email": account.Email,
			"expected_workspace": workspace, "source_account_id": account.ID, "import_operation_id": binding.OperationID,
		}
		phase := ""
		switch receipt.Phase {
		case "", "pause":
			_, saved, err := s.store.GetOAuthResultByID(ctx, account.ID)
			if err != nil {
				return err
			}
			if !store.IsSub2PaidPlan(saved.PlanType) || saved.AccessToken == "" || saved.RefreshToken == "" ||
				saved.ChatGPTAccountID == "" || saved.ExpiresAt <= time.Now().Unix() {
				return conflict
			}
			if receipt.Phase == "" {
				phase = "pause"
				if scheduled && !task.OriginalSchedulable {
					return &recoveryOperationError{Code: "manual_pause", RequiresAction: true}
				}
				status, _ := detail["status"].(string)
				message, _ := detail["error_message"].(string)
				input["explicit_enable"] = task.OriginalSchedulable && status == "error" &&
					(strings.HasPrefix(message, "Token revoked (401):") || strings.HasPrefix(message, "Unauthorized (401):") ||
						strings.HasPrefix(message, "Authentication failed (401):"))
				break
			}
			phase = "apply"
			input["credentials"] = map[string]any{
				"access_token": saved.AccessToken, "refresh_token": saved.RefreshToken, "email": account.Email,
				"chatgpt_account_id": saved.ChatGPTAccountID, "organization_id": saved.OrganizationID,
				"client_id": recoveryClientID, "plan_type": saved.PlanType, "expires_at": saved.ExpiresAt,
			}
		case "apply":
			phase = "probe"
		case "probe":
			if receipt.MayEnable && task.OriginalSchedulable {
				phase = "enable"
			}
		case "enable":
			if !receipt.MayEnable || !task.OriginalSchedulable {
				return conflict
			}
		default:
			return conflict
		}
		if phase == "" {
			message := "凭据已更新，Sub2 检测通过并保持停调"
			if scheduled {
				message = "凭据已更新，Sub2 检测通过并已开启调度"
				status, _ := detail["status"].(string)
				expiry, hasExpiry := remoteStatusTime(detail["expires_at"])
				if status != "active" || hasExpiry && !expiry.After(time.Now()) {
					message = "凭据已更新，调度开关已开启，当前不可调度"
				}
				for _, field := range []string{"rate_limit_reset_at", "overload_until", "temp_unschedulable_until"} {
					if until, ok := remoteStatusTime(detail[field]); ok && until.After(time.Now()) {
						message = "凭据已更新，调度开关已开启，当前处于冷却"
						break
					}
				}
			}
			s.setState(task.ID, store.RecoveryCompleted, message)
			return nil
		}
		input["phase"] = phase
		var next lifecycleOAuthReceipt
		if err := s.sub2JSONBody(ctx, http.MethodPost, path, input, &next); err != nil {
			reconciled, readErr := readReceipt()
			var failure *recoveryOperationError
			if phase == "probe" && errors.As(err, &failure) && accountDeactivationConfirmed(failure.Code) {
				if readErr != nil || !validReceipt(reconciled) || reconciled.Phase != "apply" ||
					!reconciled.AccountRevision.Equal(revision) || reconciled.MayEnable != receipt.MayEnable {
					return conflict
				}
				current, err := s.store.GetSub2Import(ctx, binding.DestinationKey, account.ID)
				if err != nil {
					return err
				}
				if !delivery.MatchesOriginalBinding(current) || s.store.ValidateAccountRecoveryVersion(ctx, task.ID) != nil {
					return conflict
				}
				s.handleOfficialSub2Probe(ctx, lease, current, delivery.CredentialVersion, failure)
				return nil
			}
			if readErr != nil || reconciled.Phase != phase {
				return err
			}
			next = reconciled
		}
		revisionAccepted := next.AccountRevision.After(revision) || phase == "probe" && next.AccountRevision.Equal(revision)
		if !validReceipt(next) || next.Phase != phase || !revisionAccepted {
			return conflict
		}
		receipt = next
	}
	return conflict
}
