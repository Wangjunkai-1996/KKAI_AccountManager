package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

type sub2Group struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform,omitempty"`
	Status   string `json:"status,omitempty"`
}

func (s *sub2ImportService) groups(ctx context.Context) ([]sub2Group, error) {
	var all []sub2Group
	err := s.apiJSON(ctx, http.MethodGet, "/admin/groups/all", url.Values{"platform": {"openai"}}, &all)
	if err != nil {
		return nil, err
	}
	result := make([]sub2Group, 0, len(all))
	for _, g := range all {
		if g.ID > 0 && (g.Platform == "openai" || g.Platform == "") && (g.Status == "active" || g.Status == "") {
			result = append(result, g)
		}
	}
	return result, nil
}
func (s *sub2ImportService) loadOptions(ctx context.Context, requested *store.DeliveryOptions) (store.DeliveryOptions, error) {
	options, err := s.store.GetDeliveryDefaults(ctx, s.destinationKey)
	if requested != nil {
		options = *requested
		options.GroupIDs = append([]int64{}, requested.GroupIDs...)
		options.EgressRouteIDs = append([]int64{}, requested.EgressRouteIDs...)
		err = nil
	}
	if err != nil {
		return options, err
	}
	if err = options.Validate(); err != nil {
		return options, err
	}
	return options, nil
}
func (s *sub2ImportService) resolveOptions(ctx context.Context, requested *store.DeliveryOptions) (store.DeliveryOptions, error) {
	options, err := s.loadOptions(ctx, requested)
	if err != nil {
		return options, err
	}
	if len(options.GroupIDs) > 0 {
		groups, err := s.groups(ctx)
		if err != nil {
			return options, err
		}
		ids := map[int64]bool{}
		for _, g := range groups {
			ids[g.ID] = true
		}
		for _, id := range options.GroupIDs {
			if !ids[id] {
				return options, &recoveryOperationError{Code: "group_unavailable", RequiresAction: true}
			}
		}
	}
	if len(options.EgressRouteIDs) > 0 {
		if _, err := s.resolveEgressRoutes(ctx, options.EgressRouteIDs); err != nil {
			return options, err
		}
	}
	return options, nil
}
func (s *sub2ImportService) handleImportSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		respondJSONStatus(w, 405, LoginResponse{Message: "Method not allowed"})
		return
	}
	if !s.configured() {
		respondJSONStatus(w, 503, LoginResponse{Message: "Sub2 未配置"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if r.Method == http.MethodPut {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var req *store.DeliveryOptions
		dec := json.NewDecoder(r.Body)
		if dec.Decode(&req) != nil || req == nil || dec.Decode(new(any)) != io.EOF {
			respondJSONStatus(w, 400, LoginResponse{Message: "参数格式无效"})
			return
		}
		options, err := s.resolveOptions(ctx, req)
		if err != nil {
			respondJSONStatus(w, 400, LoginResponse{Message: "分组、出口或参数无法确认，请刷新后重试"})
			return
		}
		if err = s.store.SaveDeliveryDefaults(ctx, s.destinationKey, options); err != nil {
			respondJSONStatus(w, 500, LoginResponse{Message: "默认参数保存失败"})
			return
		}
	}
	defaults, err := s.store.GetDeliveryDefaults(ctx, s.destinationKey)
	if err != nil {
		respondJSONStatus(w, 500, LoginResponse{Message: "默认参数读取失败"})
		return
	}
	groups, groupErr := s.groups(ctx)
	routes, routeErr := s.egressRoutes(ctx)
	respondJSON(w, map[string]any{"success": true, "defaults": defaults, "groups": groups, "groups_available": groupErr == nil,
		"egress_routes": routes, "egress_available": routeErr == nil})
}

// applyDeliveryGroups runs after the recovery probe, before enabling traffic.
// Only accounts created by this exact AUTH import carry an editable group intent.
func (s *sub2ImportService) applyDeliveryGroups(ctx context.Context, recoveryTask store.AccountRecoveryTask, task store.Sub2Import, detail map[string]any) error {
	if recoveryTask.Purpose != "delivery" || !matchesImportMarker(detail, task) {
		return nil
	}
	payload, err := s.store.GetSub2ImportPayload(ctx, task.ID)
	if err != nil {
		return err
	}
	var request sub2DataImportRequest
	if err = json.Unmarshal(payload, &request); err != nil || len(request.Data.Accounts) != 1 {
		return &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	marker, _ := request.Data.Accounts[0].Extra["kkai_auth_import"].(map[string]any)
	raw, selected := marker["target_group_ids"].([]any)
	if !selected {
		return nil
	} // Older and externally linked imports retain their groups.
	expected := []int64{}
	for _, id := range raw {
		expected = append(expected, int64(toFloat(id)))
	}
	slices.Sort(expected)
	// Later logins preserve administrator changes to a previously delivered account.
	prior, err := s.store.HasEarlierCompletedDelivery(ctx, recoveryTask.AccountID, recoveryTask.DeliveryID)
	if err != nil {
		return err
	}
	if prior {
		return nil
	}
	currentGroups, valid := sub2GroupIDs(detail)
	if !valid {
		return &recoveryOperationError{Code: "protocol_error"}
	}
	// A confirmed operator choice is consumed only after the remote account has
	// been rechecked. The worker never infers permission from a stale snapshot.
	if decision, decisionErr := s.store.GetAccountRecoveryGroupDecision(ctx, recoveryTask.ID); decisionErr == nil && decision.State == "confirmed" {
		if decision.Action == "keep_current" && slices.Equal(currentGroups, decision.CurrentGroupIDs) {
			return nil
		}
		if decision.Action == "use_target" && slices.Equal(currentGroups, expected) {
			return nil
		}
	}
	if slices.Equal(currentGroups, expected) {
		return nil
	}
	if len(currentGroups) > 0 {
		return recoveryGroupConflictError(currentGroups, expected)
	}
	options := store.DeliveryOptions{GroupIDs: expected, Concurrency: request.Data.Accounts[0].Concurrency, Priority: request.Data.Accounts[0].Priority}
	if _, err = s.resolveOptions(ctx, &options); err != nil {
		return err
	}
	var current map[string]any
	endpoint := "/admin/accounts/" + strconv.FormatInt(task.Sub2AccountID, 10)
	if err = s.apiJSON(ctx, http.MethodGet, endpoint, nil, &current); err != nil {
		return err
	}
	if !matchesImportMarker(current, task) || !recoveryMarkerMatches(current, recoveryTask) {
		return &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	remoteGroups, valid := sub2GroupIDs(current)
	if !valid {
		return &recoveryOperationError{Code: "protocol_error"}
	}
	if len(remoteGroups) > 0 {
		return recoveryGroupConflictError(remoteGroups, expected)
	}
	if recoveryAccountDisabled(current) {
		return &recoveryOperationError{Code: "manual_pause", RequiresAction: true}
	}
	recovery := &sub2RecoveryService{sub2: s}
	if err = recovery.sub2JSONBody(ctx, http.MethodPut, endpoint, map[string]any{"group_ids": expected}, nil); err != nil {
		return err
	}
	if err = s.apiJSON(ctx, http.MethodGet, endpoint, nil, &current); err != nil {
		return err
	}
	remoteGroups, valid = sub2GroupIDs(current)
	if !valid || len(expected) > 0 && !sub2GroupFieldsPresent(current) {
		return &recoveryOperationError{Code: "protocol_error"}
	}
	if !matchesImportMarker(current, task) || !recoveryMarkerMatches(current, recoveryTask) || !slices.Equal(remoteGroups, expected) {
		return recoveryGroupConflictError(remoteGroups, expected)
	}
	return nil
}

func sub2GroupIDs(m map[string]any) ([]int64, bool) {
	// The complete Sub2 account response uses omitempty on all three group
	// representations. If all are absent, that is a confirmed empty assignment.
	for _, field := range []struct {
		name string
		key  string
	}{
		{"group_ids", ""},
		{"groups", "id"},
		{"account_groups", "group_id"},
	} {
		raw, present := m[field.name]
		if !present || raw == nil {
			continue
		}
		values, ok := raw.([]any)
		if !ok {
			encoded, err := json.Marshal(raw)
			if err != nil || json.Unmarshal(encoded, &values) != nil {
				return nil, false
			}
		}
		ids := make([]int64, 0, len(values))
		for _, value := range values {
			if field.key != "" {
				object, ok := value.(map[string]any)
				if !ok {
					return nil, false
				}
				value = object[field.key]
			}
			number := toFloat(value)
			id := int64(number)
			if id <= 0 || float64(id) != number {
				return nil, false
			}
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return slices.Compact(ids), true
	}
	return []int64{}, true
}

func sub2GroupFieldsPresent(m map[string]any) bool {
	for _, name := range []string{"group_ids", "groups", "account_groups"} {
		if raw, ok := m[name]; ok && raw != nil {
			return true
		}
	}
	return false
}

func recoveryGroupConflictError(current, target []int64) *recoveryOperationError {
	return &recoveryOperationError{Code: "group_conflict", RequiresAction: true, Diagnostic: &store.RecoveryDiagnostic{
		CurrentGroupIDs: append([]int64(nil), current...), TargetGroupIDs: append([]int64(nil), target...),
	}}
}
