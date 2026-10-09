package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestDeliveryOptionsDefaultsSnapshotAndGroups(t *testing.T) {
	history, id := testOAuthStore(t)
	ctx := context.Background()
	groupsStatus := 200
	currentGroups := []any{}
	puts := 0
	marker := map[string]any{"source_account_id": float64(id), "operation_id": "groups-fixture"}
	detail := map[string]any{"id": float64(42), "status": "active", "schedulable": false, "group_ids": currentGroups, "extra": map[string]any{"kkai_auth_import": marker, "kkai_auth_recovery": map[string]any{"task_id": float64(99), "credential_attempt_id": float64(1)}}}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /admin/groups/all":
			if groupsStatus != 200 {
				w.WriteHeader(groupsStatus)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": []any{map[string]any{"id": 7, "name": "GPT", "platform": "openai", "status": "active"}, map[string]any{"id": 9, "platform": "claude", "status": "active"}}})
		case "GET /admin/accounts/42":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": detail})
		case "PUT /admin/accounts/42":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body) != 1 {
				t.Errorf("changed fields besides groups: %v", body)
			}
			detail["group_ids"] = body["group_ids"]
			puts++
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": detail})
		default:
			t.Errorf("unexpected remote request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	s := &sub2ImportService{store: history, baseURL: api.URL, adminAPIKey: "fixture", destinationKey: "fixture", client: api.Client()}
	save := httptest.NewRecorder()
	s.handleImportSettings(save, httptest.NewRequest(http.MethodPut, "/api/sub2/import-settings", strings.NewReader(`{"group_ids":[7,7],"priority":0,"concurrency":12,"name_prefix":"  测试池  "}`)))
	if save.Code != 200 {
		t.Fatalf("save: %d %s", save.Code, save.Body.String())
	}
	options, err := s.resolveOptions(ctx, nil)
	if err != nil || options.Priority != 0 || options.Concurrency != 12 || options.NamePrefix != "测试池" || !reflect.DeepEqual(options.GroupIDs, []int64{7}) {
		t.Fatalf("defaults: %+v %v", options, err)
	}
	delivery, err := history.QueueAccountDelivery(ctx, id, "fixture", options)
	if err != nil {
		t.Fatal(err)
	}
	if err = history.SaveDeliveryDefaults(ctx, "fixture", store.DefaultDeliveryOptions()); err != nil {
		t.Fatal(err)
	}
	frozen, err := history.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || !reflect.DeepEqual(frozen.Options, options) {
		t.Fatalf("options not frozen: %+v %v", frozen, err)
	}
	payload, operation, key, err := s.buildPayloadWithOperation(id, "groups-fixture", options)
	if err != nil {
		t.Fatal(err)
	}
	var body sub2DataImportRequest
	json.Unmarshal(payload, &body)
	if !body.SkipDefaultGroupBind || body.Data.Accounts[0].Concurrency != 12 || body.Data.Accounts[0].Priority != 0 {
		t.Fatal("new account parameters lost")
	}
	if !strings.HasPrefix(body.Data.Accounts[0].Name, "测试池_") || !strings.HasSuffix(body.Data.Accounts[0].Name, "_sub2@example.com") {
		t.Fatalf("new account prefix lost: %q", body.Data.Accounts[0].Name)
	}
	binding, _, err := history.CreateOrGetSub2Import(ctx, "fixture", id, operation, key, payload)
	if err != nil {
		t.Fatal(err)
	}
	binding.Sub2AccountID = 42
	task := store.AccountRecoveryTask{ID: 99, AccountID: id, DeliveryID: delivery.ID, Purpose: "delivery", ResultCredentialAttemptID: 1}
	groupsStatus = 503
	if err = s.applyDeliveryGroups(ctx, task, binding, detail); err == nil || classifyRecoveryError(err).RequiresAction || puts != 0 {
		t.Fatalf("503 became manual or wrote groups: %v", err)
	}
	groupsStatus = 200
	if err = s.applyDeliveryGroups(ctx, task, binding, detail); err != nil || puts != 1 {
		t.Fatalf("apply groups: puts=%d err=%v", puts, err)
	}
	// A lost response after the PUT must reconcile without another write.
	if err = s.applyDeliveryGroups(ctx, task, binding, detail); err != nil || puts != 1 {
		t.Fatalf("group retry duplicated write: %v", err)
	}
	detail["group_ids"] = []any{float64(8)}
	if err = s.applyDeliveryGroups(ctx, task, binding, detail); err == nil || puts != 1 {
		t.Fatal("unexpected existing group overwritten")
	}
	marker["operation_id"] = "external-account"
	if err = s.applyDeliveryGroups(ctx, task, binding, detail); err != nil || puts != 1 {
		t.Fatal("existing account configuration changed")
	}
}

func TestSub2ImportUnconfirmedDeadlineAndImmutableRetry(t *testing.T) {
	history, id := testOAuthStore(t)
	ctx := context.Background()
	posts := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			t.Error("uncertain import posted again")
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"items": []any{}}})
	}))
	defer api.Close()
	s := &sub2ImportService{store: history, baseURL: api.URL, adminAPIKey: "fixture", destinationKey: "fixture", client: api.Client()}
	payload, op, key, err := s.buildPayload(id, store.DeliveryOptions{GroupIDs: []int64{7}, Priority: 4, Concurrency: 8, NamePrefix: "首批"})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := history.CreateOrGetSub2Import(ctx, "fixture", id, op, key, payload)
	if err != nil {
		t.Fatal(err)
	}
	history.UpdateSub2Import(ctx, task.ID, "failed", false, 0, "fixture")
	retried, err := s.ensureTask(ctx, id, store.DefaultDeliveryOptions())
	if err != nil || retried.OperationID != op || retried.State != "queued" {
		t.Fatalf("retry: %+v %v", retried, err)
	}
	actual, err := history.GetSub2ImportPayload(ctx, task.ID)
	if err != nil || string(actual) != string(payload) {
		t.Fatal("retry changed idempotency payload")
	}
	history.UpdateSub2Import(ctx, task.ID, "unknown", false, 0, "")
	err = s.reconcile(ctx, task.ID)
	if err == nil || classifyRecoveryError(err).RequiresAction || posts != 0 {
		t.Fatalf("fresh uncertain: %v", err)
	}
	// Deadline classification is also exercised directly with the persisted clock
	// by the store fixture in account_delivery_test; do not wait wall-clock time.
	old := task
	old.CreatedAt = time.Now().Add(-16 * time.Minute)
	failure := unconfirmedImportError(old)
	if !failure.RequiresAction || failure.Code != "import_unconfirmed" {
		t.Fatalf("old uncertain: %+v", failure)
	}
}

func TestDeliveryOptionsPreserveGroupsAfterEarlierCompletedDelivery(t *testing.T) {
	history, accountID := testOAuthStore(t)
	ctx := context.Background()
	options := store.DeliveryOptions{GroupIDs: []int64{7}, Priority: 2, Concurrency: 4}
	previous, err := history.QueueAccountDelivery(ctx, accountID, "fixture", options)
	if err != nil {
		t.Fatal(err)
	}
	previousTask, _, err := history.CreateAccountDeliveryRecoveryTask(ctx, previous.ID, accountID, 42, previous.CredentialVersion, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := history.UpdateAccountDelivery(ctx, previous.ID, "verifying", "", "", nil, previousTask.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := history.UpdateAccountRecoveryTask(ctx, previousTask.ID, store.RecoveryCompleted, ""); err != nil {
		t.Fatal(err)
	}
	account, err := history.GetAccountByID(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := history.StartAttempt(ctx, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	next, err := history.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &store.Result{AccessToken: "new-fixture-at", RefreshToken: "new-fixture-rt"}, "fixture", options)
	attempt.Release()
	if err != nil {
		t.Fatal(err)
	}
	var remoteCalls atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		http.Error(w, "prior completed accounts must not query or overwrite group configuration", 503)
	}))
	defer api.Close()
	s := &sub2ImportService{store: history, baseURL: api.URL, destinationKey: "fixture", adminAPIKey: "fixture", client: api.Client()}
	payload, operationID, key, err := s.buildPayloadWithOperation(accountID, "completed-groups-fixture", options)
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := history.CreateOrGetSub2Import(ctx, "fixture", accountID, operationID, key, payload)
	if err != nil {
		t.Fatal(err)
	}
	binding.Sub2AccountID = 42
	task := store.AccountRecoveryTask{AccountID: accountID, DeliveryID: next.ID, Purpose: "delivery"}
	detail := map[string]any{"extra": map[string]any{"kkai_auth_import": map[string]any{"source_account_id": float64(accountID), "operation_id": operationID}}}
	for _, groups := range [][]any{{float64(19)}, {}} {
		detail["group_ids"] = groups
		if err := s.applyDeliveryGroups(ctx, task, binding, detail); err != nil {
			t.Fatalf("prior delivered account with administrator groups %v blocked: %v", groups, err)
		}
	}
	if got := remoteCalls.Load(); got != 0 {
		t.Fatalf("later delivery made %d group requests", got)
	}
}

func TestSub2ManualImportQueuesWithoutRemoteCallsAndFreezesOptions(t *testing.T) {
	history, accountID := testOAuthStore(t)
	var remoteCalls atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		http.Error(w, "submission must only persist the intent", 503)
	}))
	defer api.Close()
	s := &sub2ImportService{store: history, baseURL: api.URL, destinationKey: "fixture", adminAPIKey: "fixture", client: api.Client()}
	firstOptions := store.DeliveryOptions{GroupIDs: []int64{7}, Priority: 0, Concurrency: 12}
	for _, options := range []store.DeliveryOptions{firstOptions, {GroupIDs: []int64{8}, Priority: 99, Concurrency: 42}} {
		raw, err := json.Marshal(options)
		if err != nil {
			t.Fatal(err)
		}
		body := `{"account_ids":[` + strconv.FormatInt(accountID, 10) + `],"delivery_options":` + string(raw) + `}`
		response := httptest.NewRecorder()
		s.handleImport(response, httptest.NewRequest(http.MethodPost, "/api/sub2/import", strings.NewReader(body)))
		if response.Code != http.StatusOK {
			t.Fatalf("manual submission: %d %s", response.Code, response.Body.String())
		}
		var result struct {
			Success bool               `json:"success"`
			Imports []store.Sub2Import `json:"imports"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || !result.Success || len(result.Imports) != 1 || result.Imports[0].AccountID != accountID {
			t.Fatalf("accepted account response invalid: %s (%v)", response.Body.String(), err)
		}
	}
	tasks, err := history.ListDueAccountDeliveries(context.Background(), time.Now().Add(time.Minute), 10)
	if err != nil || len(tasks) != 1 || !reflect.DeepEqual(tasks[0].Options, firstOptions) {
		t.Fatalf("repeated submission changed or duplicated frozen intent: %+v %v", tasks, err)
	}
	if remoteCalls.Load() != 0 {
		t.Fatalf("queue submission made %d remote calls", remoteCalls.Load())
	}
}

func TestDeliveryRecoveryBindsGroupsOnlyAfterSuccessfulProbe(t *testing.T) {
	for _, probeOK := range []bool{true, false} {
		t.Run(strconv.FormatBool(probeOK), func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			ctx := context.Background()
			if _, err := f.store.UpdateAccountRecoveryTask(ctx, f.task.ID, store.RecoveryCanceled, "fixture setup"); err != nil {
				t.Fatal(err)
			}
			options := store.DeliveryOptions{GroupIDs: []int64{7}, Priority: 2, Concurrency: 8}
			attempt, err := f.store.StartAttempt(ctx, f.account.Email)
			if err != nil {
				t.Fatal(err)
			}
			delivery, err := f.store.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &store.Result{
				AccessToken: "delivery-at", RefreshToken: "delivery-rt", ChatGPTAccountID: "delivery-workspace", PlanType: "plus", ExpiresAt: 2100000000,
			}, "fixture", options)
			attempt.Release()
			if err != nil {
				t.Fatal(err)
			}
			// Replace only this test's placeholder binding with a real new-import
			// payload carrying the same immutable selected groups as the delivery.
			if _, err := f.db.Exec(`DELETE FROM sub2_imports WHERE account_id=?`, f.account.ID); err != nil {
				t.Fatal(err)
			}
			payload, operationID, key, err := f.service.sub2.buildPayloadWithOperation(f.account.ID, "groups-process", options)
			if err != nil {
				t.Fatal(err)
			}
			binding, _, err := f.store.CreateOrGetSub2Import(ctx, "fixture", f.account.ID, operationID, key, payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.store.UpdateSub2Import(ctx, binding.ID, "imported", true, 901, ""); err != nil {
				t.Fatal(err)
			}
			f.probeOK = probeOK
			groups := []any{}
			// Wrap the existing OAuth fixture only for its new groups contract;
			// credential application, SSE probe and enabling use the real process.
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/admin/groups/all" {
					writeRecoveryEnvelope(w, []any{map[string]any{"id": 7, "name": "GPT", "platform": "openai", "status": "active"}})
					return
				}
				if r.Method == http.MethodPut && r.URL.Path == "/admin/accounts/901" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					if f.testCalls != 1 || !f.probeOK || f.schedule {
						t.Errorf("groups changed before a successful paused-account probe: tested=%d passed=%v schedulable=%v", f.testCalls, f.probeOK, f.schedule)
					}
					groups, _ = body["group_ids"].([]any)
					if len(body) != 1 || !reflect.DeepEqual(groups, []any{float64(7)}) {
						t.Errorf("unexpected group mutation: %v", body)
					}
					f.events = append(f.events, "group")
					writeRecoveryEnvelope(w, map[string]any{})
					return
				}
				if r.Method == http.MethodGet && r.URL.Path == "/admin/accounts/901" {
					recorder := httptest.NewRecorder()
					f.handle(recorder, r)
					var envelope map[string]any
					if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
						t.Error(err)
					}
					f.mu.Lock()
					defer f.mu.Unlock()
					detail := envelope["data"].(map[string]any)
					detail["group_ids"] = groups
					detail["extra"].(map[string]any)["kkai_auth_import"].(map[string]any)["operation_id"] = operationID
					writeRecoveryEnvelope(w, detail)
					return
				}
				f.handle(w, r)
			}))
			defer api.Close()
			f.service.sub2.baseURL, f.service.sub2.client = api.URL, api.Client()
			service := newAccountDeliveryService(f.store, f.service.sub2, f.service)
			defer service.Stop()
			service.process(ctx, delivery)
			delivery, err = f.store.GetAccountDelivery(ctx, delivery.ID)
			if err != nil || delivery.RecoveryTaskID == 0 {
				t.Fatalf("delivery did not hand off: %+v %v", delivery, err)
			}
			task, err := f.store.ClaimAccountRecoveryTask(ctx, delivery.RecoveryTaskID)
			if err != nil {
				t.Fatal(err)
			}
			f.service.process(task)
			task, err = f.store.GetAccountRecoveryTaskByID(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			events := strings.Join(f.events, ",")
			if probeOK {
				if task.State != store.RecoveryCompleted || !strings.Contains(events, "apply,test,group,enable") || !f.schedule {
					t.Fatalf("expected probe -> group -> enable: state=%s events=%s scheduled=%v", task.State, events, f.schedule)
				}
			} else if task.State == store.RecoveryCompleted || strings.Contains(events, "group") || strings.Contains(events, "enable") || f.schedule {
				t.Fatalf("failed probe leaked group assignment or traffic: state=%s events=%s scheduled=%v", task.State, events, f.schedule)
			}
		})
	}
}
