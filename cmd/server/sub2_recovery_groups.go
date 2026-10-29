package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

type recoveryGroupActionRequest struct {
	Action          string   `json:"action"`
	CurrentGroupIDs *[]int64 `json:"current_group_ids"`
	TargetGroupIDs  *[]int64 `json:"target_group_ids"`
}

// handleRecoveryGroupAction accepts the explicit choice shown for a delivery
// group conflict. Every action re-reads and re-validates the remote account;
// the stored diagnostic is only a CAS snapshot, never permission to overwrite
// a changed account.
func (s *sub2RecoveryService) handleRecoveryGroupAction(w http.ResponseWriter, r *http.Request, taskID int64) {
	if s == nil || s.store == nil || s.sub2 == nil || !s.sub2.configured() {
		recoveryAPIError(w, http.StatusServiceUnavailable, "recovery_unavailable", "Sub2 恢复服务未配置")
		return
	}
	if r.Method != http.MethodPost {
		recoveryAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
		return
	}
	var req recoveryGroupActionRequest
	if !decodeRecoveryJSON(w, r, &req) {
		return
	}
	action := strings.TrimSpace(req.Action)
	if action == "apply_target" {
		action = "use_target"
	}
	if action != "keep_current" && action != "use_target" || req.CurrentGroupIDs == nil || req.TargetGroupIDs == nil {
		recoveryAPIError(w, http.StatusBadRequest, "invalid_group_action", "分组操作或快照无效")
		return
	}
	currentIDs, targetIDs := normalizeGroupIDs(*req.CurrentGroupIDs), normalizeGroupIDs(*req.TargetGroupIDs)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	requestedID := taskID
	task, err := s.store.GetAccountRecoveryTaskByIDForDestination(ctx, taskID, s.sub2.destinationKey)
	// The history card normally sends recovery_task_id. Accepting the delivery
	// ID as a fallback keeps older cached history cards actionable without
	// weakening the destination and task identity checks below.
	if errors.Is(err, store.ErrAccountRecoveryNotFound) {
		if delivery, deliveryErr := s.store.GetAccountDelivery(ctx, taskID); deliveryErr == nil && delivery.DestinationKey == s.sub2.destinationKey && delivery.RecoveryTaskID > 0 {
			taskID = delivery.RecoveryTaskID
			task, err = s.store.GetAccountRecoveryTaskByIDForDestination(ctx, taskID, s.sub2.destinationKey)
		}
	}
	if err != nil {
		if errors.Is(err, store.ErrAccountRecoveryNotFound) {
			recoveryAPIError(w, http.StatusNotFound, "recovery_not_found", "恢复任务不存在")
		} else {
			recoveryAPIError(w, http.StatusInternalServerError, "recovery_read_failed", "读取恢复任务失败")
		}
		return
	}
	if task.Purpose != "delivery" || task.ErrorCode != "group_conflict" || task.Diagnostic == nil ||
		!slices.Equal(normalizeGroupIDs(task.Diagnostic.CurrentGroupIDs), currentIDs) ||
		!slices.Equal(normalizeGroupIDs(task.Diagnostic.TargetGroupIDs), targetIDs) {
		recoveryAPIError(w, http.StatusConflict, "group_conflict_stale", "分组状态已变化，请刷新后重新选择")
		return
	}
	lease, err := s.store.AcquireAccountRecovery(ctx, task.AccountID)
	if err != nil {
		recoveryAPIError(w, http.StatusConflict, "recovery_busy", "账号正在处理，请稍后重试")
		return
	}
	defer lease.Release()
	if err := s.store.ValidateAccountRecoveryVersion(ctx, task.ID); err != nil {
		recoveryAPIError(w, http.StatusConflict, "recovery_version_changed", "账号凭据已变化，请重新检测后处理")
		return
	}
	account, err := s.store.GetAccountByID(ctx, task.AccountID)
	if err != nil {
		recoveryAPIError(w, http.StatusConflict, "group_conflict_identity_changed", "账号绑定已变化，请重新核对")
		return
	}
	binding, err := s.sub2.store.GetSub2Import(ctx, s.sub2.destinationKey, task.AccountID)
	if err != nil || binding.State != "imported" || binding.Sub2AccountID != task.Sub2AccountID {
		recoveryAPIError(w, http.StatusConflict, "group_conflict_identity_changed", "Sub2 绑定已变化，请重新核对")
		return
	}
	detail, err := s.sub2Account(ctx, task.Sub2AccountID)
	if err != nil {
		recoveryAPIError(w, http.StatusBadGateway, "group_conflict_remote_read_failed", "读取 Sub2 账号失败，请稍后重试")
		return
	}
	if verifySub2RecoveryDetail(detail, task.Sub2AccountID, task.AccountID, account, binding) != nil || !recoveryMarkerMatches(detail, task) || !matchesImportMarker(detail, binding) {
		recoveryAPIError(w, http.StatusConflict, "group_conflict_identity_changed", "Sub2 账号身份或恢复凭据已变化，请重新核对")
		return
	}
	remoteGroups, valid := sub2GroupIDs(detail)
	if !valid {
		recoveryAPIError(w, http.StatusConflict, "group_conflict_remote_read_failed", "Sub2 当前分组格式无法确认，请刷新后重试")
		return
	}
	if !slices.Equal(remoteGroups, currentIDs) && !slices.Equal(remoteGroups, targetIDs) {
		recoveryAPIError(w, http.StatusConflict, "group_conflict_stale", "Sub2 当前分组已变化，请刷新后重新选择")
		return
	}
	if action == "keep_current" && !slices.Equal(remoteGroups, currentIDs) {
		recoveryAPIError(w, http.StatusConflict, "group_conflict_stale", "Sub2 当前分组已变化，请刷新后重新选择")
		return
	}
	if action == "use_target" {
		options := store.DeliveryOptions{GroupIDs: targetIDs, Priority: 0, Concurrency: 1}
		if _, err := s.sub2.resolveOptions(ctx, &options); err != nil {
			failure := classifyRecoveryError(err)
			recoveryAPIError(w, http.StatusConflict, failure.Code, failure.Error())
			return
		}
		if _, err := s.store.BeginAccountRecoveryGroupDecision(ctx, task.ID, action, currentIDs, targetIDs, task.UpdatedAt.UnixMilli()); err != nil {
			recoveryGroupActionAPIError(w, err)
			return
		}
		if !slices.Equal(remoteGroups, targetIDs) {
			endpoint := "/admin/accounts/" + strconv.FormatInt(task.Sub2AccountID, 10)
			if err := (&sub2RecoveryService{sub2: s.sub2}).sub2JSONBody(ctx, http.MethodPut, endpoint, map[string]any{"group_ids": targetIDs}, nil); err != nil {
				recoveryGroupActionAPIError(w, err)
				return
			}
			detail, err = s.sub2Account(ctx, task.Sub2AccountID)
			remoteGroups, valid := sub2GroupIDs(detail)
			if err != nil || !valid || verifySub2RecoveryDetail(detail, task.Sub2AccountID, task.AccountID, account, binding) != nil || !recoveryMarkerMatches(detail, task) || !matchesImportMarker(detail, binding) || len(targetIDs) > 0 && !sub2GroupFieldsPresent(detail) || !slices.Equal(remoteGroups, targetIDs) {
				recoveryAPIError(w, http.StatusConflict, "group_conflict_unconfirmed", "目标分组写入结果无法确认，请刷新后重试")
				return
			}
		}
	} else if _, err := s.store.BeginAccountRecoveryGroupDecision(ctx, task.ID, action, currentIDs, targetIDs, task.UpdatedAt.UnixMilli()); err != nil {
		recoveryGroupActionAPIError(w, err)
		return
	}
	updated, err := s.store.ConfirmAccountRecoveryGroupDecision(ctx, task.ID, action, currentIDs, targetIDs, task.UpdatedAt.UnixMilli())
	if err != nil {
		recoveryGroupActionAPIError(w, err)
		return
	}
	s.notify()
	respondJSONStatus(w, http.StatusAccepted, map[string]any{"success": true, "action": action, "task": updated, "requested_id": requestedID})
}

func normalizeGroupIDs(ids []int64) []int64 {
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			result = append(result, id)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func recoveryGroupActionAPIError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrAccountRecoveryStale) {
		recoveryAPIError(w, http.StatusConflict, "group_conflict_stale", "分组状态已变化，请刷新后重新选择")
		return
	}
	if errors.Is(err, store.ErrAccountRecoveryNotResumable) || strings.Contains(err.Error(), "no longer actionable") {
		recoveryAPIError(w, http.StatusConflict, "group_conflict_stale", "该分组冲突已处理或已失效")
		return
	}
	failure := classifyRecoveryError(err)
	recoveryAPIError(w, http.StatusBadGateway, failure.Code, failure.Error())
}
