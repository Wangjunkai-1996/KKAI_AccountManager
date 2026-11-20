package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

type sub2EgressRoute struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Protocol   string `json:"protocol,omitempty"`
	State      string `json:"state"`
	ProxyID    *int64 `json:"proxy_id,omitempty"`
	Eligible   bool   `json:"eligible"`
	PublicIP   string `json:"public_ip,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
}

type sub2Proxy struct {
	Protocol  string     `json:"protocol"`
	Host      string     `json:"host"`
	Port      int        `json:"port"`
	Username  string     `json:"username"`
	Password  string     `json:"password"`
	Status    string     `json:"status"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type resolvedEgressRoute struct {
	RouteID  int64
	ProxyURL string
	RouteKey string
}

func (s *sub2ImportService) egressRoutes(ctx context.Context) ([]sub2EgressRoute, error) {
	var catalog struct {
		Items []sub2EgressRoute `json:"items"`
	}
	if s == nil || !s.configured() {
		return nil, &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	if err := s.apiJSON(ctx, http.MethodGet, "/admin/egress-routes/assignable", nil, &catalog); err != nil {
		return nil, classifyRecoveryError(err)
	}
	return catalog.Items, nil
}

func (s *sub2ImportService) resolveEgressRoutes(ctx context.Context, ids []int64) ([]resolvedEgressRoute, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	routes, err := s.egressRoutes(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]sub2EgressRoute, len(routes))
	for _, route := range routes {
		byID[route.ID] = route
	}
	result := make([]resolvedEgressRoute, 0, len(ids))
	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, errors.New("Sub2 出口选择无效")
		}
		seen[id] = true
		route, err := s.resolveEgressRoute(ctx, byID[id])
		if err != nil {
			return nil, err
		}
		result = append(result, route)
	}
	return result, nil
}

func (s *sub2ImportService) resolveEgressRoute(ctx context.Context, route sub2EgressRoute) (resolvedEgressRoute, error) {
	var resolved resolvedEgressRoute
	if route.ID <= 0 || !route.Eligible || route.State != "active" || route.Kind != "proxy" || route.ProxyID == nil || *route.ProxyID <= 0 || !strings.EqualFold(route.Protocol, "http") {
		return resolved, &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
	}
	var proxy sub2Proxy
	path := "/admin/proxies/" + strconv.FormatInt(*route.ProxyID, 10)
	if err := s.apiJSON(ctx, http.MethodGet, path, nil, &proxy); err != nil {
		failure := classifyRecoveryError(err)
		if failure.HTTPStatus == http.StatusNotFound {
			failure.Code = "egress_unavailable"
		}
		return resolved, failure
	}
	if proxy.Status != "active" || proxy.ExpiresAt != nil && !proxy.ExpiresAt.After(time.Now()) || !strings.EqualFold(proxy.Protocol, "http") || strings.TrimSpace(proxy.Host) == "" || proxy.Port < 1 || proxy.Port > 65535 {
		return resolved, &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
	}
	u := &url.URL{Scheme: "http", Host: net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port))}
	if proxy.Username != "" || proxy.Password != "" {
		if proxy.Username == "" || proxy.Password == "" {
			return resolved, &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
		}
		u.User = url.UserPassword(proxy.Username, proxy.Password)
	}
	proxyURL := u.String()
	if login.ValidateHTTPProxy(proxyURL) != nil {
		return resolved, &recoveryOperationError{Code: "egress_unavailable", RequiresAction: true}
	}
	return resolvedEgressRoute{
		RouteID: route.ID, ProxyURL: proxyURL,
		RouteKey: fmt.Sprintf("%s|%s|%d|%s|%s", strings.TrimSpace(proxy.Protocol), strings.TrimSpace(proxy.Host), proxy.Port, strings.TrimSpace(proxy.Username), strings.TrimSpace(proxy.Password)),
	}, nil
}

func (s *sub2ImportService) resolveLoginEgress(ctx context.Context, options *store.DeliveryOptions) (string, error) {
	if options == nil || len(options.EgressRouteIDs) == 0 {
		return "", nil
	}
	routes, err := s.resolveEgressRoutes(ctx, options.EgressRouteIDs[:1])
	if err != nil {
		return "", err
	}
	return routes[0].ProxyURL, nil
}

func loadDefaultLoginDeliveryOptions(ctx context.Context, req *LoginRequest) error {
	if req.DeliveryOptions != nil || !req.AutoDeliver && strings.TrimSpace(req.Proxy) != "" || sub2Importer == nil || !sub2Importer.configured() {
		return nil
	}
	options, err := sub2Importer.loadOptions(ctx, nil)
	if err != nil {
		return errors.New("Sub2 交付默认参数无法读取")
	}
	if req.AutoDeliver || len(options.EgressRouteIDs) > 0 {
		req.DeliveryOptions = &options
	}
	return nil
}

func applyLoginEgress(ctx context.Context, req *LoginRequest) error {
	if req.DeliveryOptions == nil {
		return nil
	}
	if err := req.DeliveryOptions.Validate(); err != nil {
		return err
	}
	if len(req.DeliveryOptions.EgressRouteIDs) == 0 {
		return nil
	}
	if sub2Importer == nil || !sub2Importer.configured() {
		return errors.New("Sub2 未配置")
	}
	proxy, err := sub2Importer.resolveLoginEgress(ctx, req.DeliveryOptions)
	if err != nil {
		return err
	}
	req.Proxy = proxy
	req.UpstreamProxy = ""
	return nil
}
