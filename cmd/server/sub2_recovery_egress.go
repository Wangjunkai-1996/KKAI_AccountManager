package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

// Prefer Sub2's current proxies, then IPv4 direct after connectivity failures.
// This selects the AUTH login exit only; Sub2's egress configuration is unchanged.
func (s *sub2ImportService) boundAccountLoginProxy(ctx context.Context, account store.Account, binding store.Sub2Import, savedProxy string, check func(context.Context, string) (login.ProxyCheckResult, error)) (string, error) {
	if binding.State != "imported" || binding.Sub2AccountID <= 0 || binding.AccountID != account.ID {
		return "", &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(withSub2StatusFresh(ctx), 30*time.Second)
	defer cancel()
	var detail map[string]any
	err := s.apiJSON(ctx, http.MethodGet, "/admin/accounts/"+strconv.FormatInt(binding.Sub2AccountID, 10), nil, &detail)
	if err != nil {
		return "", classifyRecoveryError(err)
	}
	if verifySub2RecoveryIdentity(detail, binding.Sub2AccountID, account.ID, account, binding) != nil {
		return "", &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	rawMode, hasMode := detail["egress_mode"]
	mode, validMode := rawMode.(string)
	_, hasPool := detail["egress_pool"]
	proxyID, hasProxy := detail["proxy_id"]
	if !hasMode && !hasPool && !hasProxy {
		return savedProxy, nil // Older Sub2 responses did not expose an egress contract.
	}
	if hasMode && !validMode {
		return "", &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
	}
	var routeIDs []int64
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "pool":
		var pool struct {
			RouteIDs       []int64 `json:"route_ids"`
			PrimaryRouteID int64   `json:"primary_route_id"`
		}
		encoded, err := json.Marshal(detail["egress_pool"])
		if err != nil || json.Unmarshal(encoded, &pool) != nil || pool.PrimaryRouteID <= 0 || !slices.Contains(pool.RouteIDs, pool.PrimaryRouteID) {
			return "", &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
		}
		routeIDs = append([]int64{pool.PrimaryRouteID}, pool.RouteIDs...)
	case "", "legacy":
		id := int64(toFloat(proxyID))
		if id <= 0 || !hasProxy || detail["egress_pool"] != nil {
			return "", &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
		}
	default:
		return "", &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
	}
	routes, err := s.egressRoutes(ctx)
	if err != nil {
		return "", err
	}
	byID := make(map[int64]sub2EgressRoute, len(routes))
	for _, route := range routes {
		byID[route.ID] = route
		if len(routeIDs) == 0 && route.ProxyID != nil && *route.ProxyID == int64(toFloat(proxyID)) {
			routeIDs = []int64{route.ID}
		}
	}
	if check == nil {
		return "", &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	seen := make(map[int64]bool, len(routeIDs))
	var transient *recoveryOperationError
	connectivityFailed := false
	for _, id := range routeIDs {
		if ctx.Err() != nil {
			transient = classifyRecoveryError(ctx.Err())
			break
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		route, err := s.resolveEgressRoute(ctx, byID[id])
		if err != nil {
			failure := classifyRecoveryError(err)
			if failure.RequiresAction && failure.Code != "egress_unavailable" {
				return "", failure
			}
			if !failure.RequiresAction && (transient == nil || failure.RetryAfterSeconds > transient.RetryAfterSeconds) {
				transient = failure
			}
			continue
		}
		result, err := check(ctx, route.ProxyURL)
		if err == nil && result.Reachable {
			return route.ProxyURL, nil
		}
		// Proxy authentication is a configuration failure; a connection failure
		// is temporary even when CheckProxy reports it without an error value.
		if result.HTTPStatus == http.StatusProxyAuthRequired {
			continue
		}
		connectivityFailed = true
		failure := classifyRecoveryError(err)
		if transient == nil || failure.RetryAfterSeconds > transient.RetryAfterSeconds {
			transient = failure
		}
	}
	if parent.Err() != nil {
		return "", classifyRecoveryError(parent.Err())
	}
	if connectivityFailed {
		// Give the fallback its own bounded probe while retaining caller cancellation.
		fallbackCtx, stop := context.WithTimeout(parent, 10*time.Second)
		defer stop()
		result, err := check(fallbackCtx, "direct")
		if fallbackCtx.Err() != nil {
			return "", classifyRecoveryError(fallbackCtx.Err())
		}
		if err == nil && result.Reachable {
			log.Printf("AUTH 登录出口回退到本机 IPv4: account_id=%d sub2_account_id=%d", account.ID, binding.Sub2AccountID)
			return "direct", nil
		}
	}
	if transient != nil {
		return "", transient
	}
	return "", &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
}

func (s *sub2RecoveryService) recoveryLoginProxy(ctx context.Context, account store.Account, taskID int64, savedProxy string) (string, error) {
	if s.sub2 == nil || !s.sub2.configured() {
		return savedProxy, nil
	}
	task, err := s.store.GetAccountRecoveryTaskByID(ctx, taskID)
	if err != nil {
		return "", err
	}
	binding, err := s.store.GetSub2Import(ctx, s.sub2.destinationKey, account.ID)
	if err != nil {
		return "", err
	}
	if task.AccountID != account.ID || task.Sub2AccountID != binding.Sub2AccountID || task.DestinationKey != "" && task.DestinationKey != s.sub2.destinationKey {
		return "", &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	return s.sub2.boundAccountLoginProxy(ctx, account, binding, savedProxy, s.checkLoginProxy)
}

func (s *sub2ImportService) accountLoginProxy(ctx context.Context, account store.Account, options *store.DeliveryOptions, savedProxy string, check func(context.Context, string) (login.ProxyCheckResult, error)) (string, error) {
	if s == nil || !s.configured() {
		return savedProxy, nil
	}
	binding, err := s.store.GetSub2Import(ctx, s.destinationKey, account.ID)
	if err == nil && binding.State == "imported" {
		return s.boundAccountLoginProxy(ctx, account, binding, savedProxy, check)
	}
	if err != nil && !errors.Is(err, store.ErrSub2ImportNotFound) {
		return "", err
	}
	if options == nil {
		defaults, err := s.loadOptions(ctx, nil)
		if err != nil {
			return "", err
		}
		options = &defaults
	}
	proxy, err := s.resolveLoginEgress(ctx, options)
	if err != nil {
		return "", err
	}
	if proxy != "" {
		return proxy, nil
	}
	// An unbound or remote-missing account may retain a stale historical
	// proxy. Probe it before login; if only that exit is broken, use the
	// server's direct IPv4 path when it is reachable. Explicit Sub2 egress
	// choices have already returned above and never take this fallback.
	if strings.TrimSpace(savedProxy) != "" && check != nil {
		if login.ValidateLoginProxy(savedProxy) != nil {
			return "", &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
		}
		probeCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		result, probeErr := check(probeCtx, savedProxy)
		probeContextErr := probeCtx.Err()
		stop()
		if ctx.Err() != nil {
			return "", classifyRecoveryError(ctx.Err())
		}
		if probeContextErr == nil && probeErr == nil && result.Reachable {
			return savedProxy, nil
		}
		if result.HTTPStatus == http.StatusProxyAuthRequired {
			return "", &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
		}
		if strings.EqualFold(strings.TrimSpace(savedProxy), "direct") {
			return "", classifyRecoveryError(errors.Join(probeContextErr, probeErr))
		}
		directCtx, directStop := context.WithTimeout(ctx, 10*time.Second)
		direct, directErr := check(directCtx, "direct")
		directContextErr := directCtx.Err()
		directStop()
		if ctx.Err() != nil {
			return "", classifyRecoveryError(ctx.Err())
		}
		if directContextErr == nil && directErr == nil && direct.Reachable {
			log.Printf("AUTH 登录出口回退到本机 IPv4: account_id=%d", account.ID)
			return "direct", nil
		}
		return "", classifyRecoveryError(errors.Join(directContextErr, directErr))
	}
	return savedProxy, nil
}
