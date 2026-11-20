package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestSub2SelectedEgressImportReadback(t *testing.T) {
	for _, tc := range []struct {
		name        string
		actualIDs   []int64
		actualFirst int64
		actualLimit int
		warning     bool
		imported    bool
	}{
		{name: "matching pool", actualIDs: []int64{11, 12}, actualFirst: 11, actualLimit: 3, imported: true},
		{name: "wrong pool", actualIDs: []int64{11}, actualFirst: 11, actualLimit: 3},
		{name: "wrong primary", actualIDs: []int64{11, 12}, actualFirst: 12, actualLimit: 3},
		{name: "wrong concurrency", actualIDs: []int64{11, 12}, actualFirst: 11, actualLimit: 4},
		{name: "sub2 fallback warning", actualIDs: []int64{11}, actualFirst: 11, actualLimit: 3, warning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			history, accountID := testOAuthStore(t)
			posts := 0
			var posted sub2DataImportRequest
			var operationID string
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("x-api-key") != "secret" {
					t.Errorf("missing admin key on %s", r.URL.Path)
				}
				switch r.Method + " " + r.URL.Path {
				case "GET /api/v1/admin/egress-routes/assignable":
					fmt.Fprint(w, `{"code":0,"data":{"items":[{"id":11,"kind":"proxy","state":"active","eligible":true,"proxy_id":21,"protocol":"http"},{"id":12,"kind":"proxy","state":"active","eligible":true,"proxy_id":22,"protocol":"http"}]}}`)
				case "GET /api/v1/admin/proxies/21":
					fmt.Fprint(w, `{"code":0,"data":{"protocol":"http","host":"proxy-1.test","port":8080,"username":"user","password":"pass","status":"active"}}`)
				case "GET /api/v1/admin/proxies/22":
					fmt.Fprint(w, `{"code":0,"data":{"protocol":"http","host":"proxy-2.test","port":8081,"username":"user2","password":"pass2","status":"active"}}`)
				case "POST /api/v1/admin/accounts/data":
					posts++
					if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
						t.Error(err)
					}
					operationID, _ = posted.Data.Accounts[0].Extra["kkai_auth_import"].(map[string]any)["operation_id"].(string)
					if tc.warning {
						fmt.Fprint(w, `{"code":0,"data":{"account_created":1,"account_failed":0,"warnings":[{"code":"EGRESS_POOL_NOT_RESTORED"}]}}`)
					} else {
						fmt.Fprint(w, `{"code":0,"data":{"account_created":1,"account_failed":0}}`)
					}
				case "GET /api/v1/admin/accounts":
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []any{map[string]any{"id": 42, "extra": map[string]any{"kkai_auth_import": map[string]any{"operation_id": operationID, "source_account_id": accountID}}}}}})
				case "GET /api/v1/admin/accounts/42":
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{
						"id": 42, "egress_mode": "pool", "egress_pool": map[string]any{"route_ids": tc.actualIDs, "primary_route_id": tc.actualFirst, "concurrency_per_egress": tc.actualLimit},
						"extra": map[string]any{"kkai_auth_import": map[string]any{"operation_id": operationID, "source_account_id": accountID}},
					}})
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer api.Close()
			svc := &sub2ImportService{store: history, baseURL: api.URL + "/api/v1", adminAPIKey: "secret", destinationKey: "test", client: api.Client()}
			options := store.DeliveryOptions{EgressRouteIDs: []int64{11, 12}, Priority: 1, Concurrency: 3, Merchant: "merchant", Deadline: "2026-12-31"}
			payload, op, key, err := svc.buildPayload(accountID, options)
			if err != nil {
				t.Fatal(err)
			}
			var built sub2DataImportRequest
			if err := json.Unmarshal(payload, &built); err != nil {
				t.Fatal(err)
			}
			pool := built.Data.Accounts[0].EgressPool
			if built.Data.Version != 2 || len(built.Data.Proxies) != 0 || pool == nil || built.Data.Accounts[0].EgressMode != "pool" ||
				!reflect.DeepEqual(pool.RouteKeys, []string{"http|proxy-1.test|8080|user|pass", "http|proxy-2.test|8081|user2|pass2"}) ||
				pool.PrimaryRouteKey != pool.RouteKeys[0] || pool.ConcurrencyPerEgress != 3 {
				t.Fatalf("invalid selected pool payload: %+v", built.Data.Accounts[0])
			}
			task, _, err := history.CreateOrGetSub2Import(context.Background(), "test", accountID, op, key, payload)
			if err != nil {
				t.Fatal(err)
			}
			err = svc.processQueued(context.Background(), task)
			got, getErr := history.GetSub2ImportByID(context.Background(), task.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if tc.imported {
				if err != nil || got.State != "imported" || got.Sub2AccountID != 42 {
					t.Fatalf("expected imported; task=%+v err=%v", got, err)
				}
			} else if err == nil || classifyRecoveryError(err).Code != "egress_mismatch" || !classifyRecoveryError(err).RequiresAction || got.State != "unknown" || !got.CreateAcknowledged {
				t.Fatalf("expected manual mismatch; task=%+v err=%v", got, err)
			}
			if !reflect.DeepEqual(posted.Data.Accounts[0].EgressPool, pool) || posts != 1 {
				t.Fatalf("unexpected import post count=%d body=%+v", posts, posted.Data.Accounts[0])
			}
			if !tc.imported {
				_, _ = svc.ensureTask(context.Background(), accountID, options)
				if posts != 1 {
					t.Fatal("mismatch triggered a second import")
				}
			}
		})
	}
}

func TestSub2ImportWithoutEgressKeepsLegacyPayload(t *testing.T) {
	history, accountID := testOAuthStore(t)
	svc := &sub2ImportService{store: history, destinationKey: "test"}
	payload, _, _, err := svc.buildPayload(accountID, store.DeliveryOptions{
		Priority: 1, Concurrency: 3, Merchant: "merchant", Deadline: "2026-12-31",
	})
	if err != nil {
		t.Fatal(err)
	}
	var request sub2DataImportRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		t.Fatal(err)
	}
	if request.Data.Version != 1 || request.Data.Accounts[0].EgressMode != "" || request.Data.Accounts[0].EgressPool != nil {
		t.Fatalf("legacy delivery changed: %+v", request.Data.Accounts[0])
	}
}
