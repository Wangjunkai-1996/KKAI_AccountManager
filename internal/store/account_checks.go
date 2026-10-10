package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const AccountCheckModel = "gpt-5.6-luna"

// AccountCheckError describes safe request errors, never database details.
type AccountCheckError struct {
	Code              string  `json:"code"`
	Message           string  `json:"message"`
	ActiveBatchID     string  `json:"active_batch_id,omitempty"`
	InvalidAccountIDs []int64 `json:"invalid_account_ids,omitempty"`
}

func (e *AccountCheckError) Error() string  { return e.Message }
func checkError(code, message string) error { return &AccountCheckError{Code: code, Message: message} }

type AccountCheckInput struct {
	RequestKey  string
	AccountIDs  []int64
	Concurrency int
	ProxyMode   string
	Proxy       string
	// DestinationKey binds eligibility and recovery to one Sub2 target.
	// Empty preserves the legacy behavior for direct store callers.
	DestinationKey string
}
type AccountCheckCounts struct {
	Total       int `json:"total"`
	Queued      int `json:"queued"`
	Running     int `json:"running"`
	Finished    int `json:"finished"`
	Skipped     int `json:"skipped"`
	Canceled    int `json:"canceled"`
	Interrupted int `json:"interrupted"`
	OK          int `json:"ok"`
	Abnormal    int `json:"abnormal"`
	Settled     int `json:"settled"`
	Precheck    int `json:"precheck"`
	Attempted   int `json:"attempted"`
}
type AccountCheckBatch struct {
	ID              string             `json:"id"`
	State           string             `json:"state"`
	StopReason      string             `json:"stop_reason,omitempty"`
	Model           string             `json:"model"`
	ProtocolVersion int                `json:"protocol_version"`
	Concurrency     int                `json:"concurrency"`
	ProxyMode       string             `json:"proxy_mode"`
	RouteLabel      string             `json:"route_label"`
	DestinationKey  string             `json:"-"`
	CreatedAt       int64              `json:"created_at"`
	FinishedAt      *int64             `json:"finished_at"`
	Counts          AccountCheckCounts `json:"counts"`
}
type AccountCheck struct {
	Model               string `json:"model"`
	RouteLabel          string `json:"route_label"`
	ID                  int64  `json:"id"`
	BatchID             string `json:"batch_id"`
	AccountID           *int64 `json:"account_id"`
	State               string `json:"state"`
	CredentialAttemptID *int64 `json:"credential_attempt_id"`
	Freshness           string `json:"freshness"`
	Outcome             string `json:"outcome,omitempty"`
	SkipReason          string `json:"skip_reason,omitempty"`
	RequestAttempted    *bool  `json:"request_attempted"`
	HTTPStatus          *int   `json:"http_status"`
	ProxyHTTPStatus     *int   `json:"proxy_http_status"`
	StreamErrorStatus   *int   `json:"stream_error_status"`
	ErrorCode           string `json:"error_code,omitempty"`
	StreamErrorCode     string `json:"stream_error_code,omitempty"`
	Message             string `json:"message,omitempty"`
	FailureStage        string `json:"failure_stage,omitempty"`
	RetryAfterSeconds   *int   `json:"retry_after_seconds"`
	PreviousCheckID     *int64 `json:"previous_check_id"`
	CreatedAt           int64  `json:"created_at"`
	StartedAt           *int64 `json:"started_at"`
	FinishedAt          *int64 `json:"finished_at"`
	DurationMS          int64  `json:"duration_ms"`
}

// Work credentials are deliberately excluded from JSON and never persisted in checks.
type AccountCheckWork struct {
	Batch            AccountCheckBatch   `json:"batch"`
	Check            AccountCheck        `json:"check"`
	AccessToken      string              `json:"-"`
	ChatGPTAccountID string              `json:"-"`
	Proxy            string              `json:"-"`
	PrecheckResult   *AccountCheckResult `json:"-"`
}
type AccountCheckResult struct {
	RequestAttempted  *bool
	Outcome           string
	HTTPStatus        *int
	ProxyHTTPStatus   *int
	StreamErrorStatus *int
	ErrorCode         string
	StreamErrorCode   string
	Message           string
	FailureStage      string
	RetryAfterSeconds *int
	DurationMS        int64
	Canceled          bool
}
type AccountCheckSummary struct {
	AccountID                int64         `json:"account_id"`
	Eligible                 bool          `json:"eligible"`
	IneligibleReason         string        `json:"ineligible_reason,omitempty"`
	CooldownRemainingSeconds int           `json:"cooldown_remaining_seconds"`
	LatestTask               *AccountCheck `json:"latest_task"`
	LastResult               *AccountCheck `json:"last_result"`
}

func (s *Store) migrateAccountChecks() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS account_check_batches (
 id TEXT PRIMARY KEY, request_key TEXT UNIQUE NOT NULL, request_hash TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('active','stopping','completed','stopped')),
 stop_reason TEXT NOT NULL DEFAULT '', model TEXT NOT NULL, protocol_version INTEGER NOT NULL,
 concurrency INTEGER NOT NULL CHECK(concurrency IN (1,2)),
 destination_key TEXT NOT NULL DEFAULT '',
 proxy_mode TEXT NOT NULL CHECK(proxy_mode IN ('default','direct')), route_label TEXT NOT NULL,
 proxy_cipher BLOB, created_at INTEGER NOT NULL, finished_at INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS account_check_single_active ON account_check_batches((1)) WHERE state IN ('active','stopping');
CREATE TABLE IF NOT EXISTS account_checks (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 batch_id TEXT NOT NULL REFERENCES account_check_batches(id),
 account_id INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
 state TEXT NOT NULL CHECK(state IN ('queued','running','finished','skipped','canceled','interrupted')),
 credential_attempt_id INTEGER, outcome TEXT NOT NULL DEFAULT '', skip_reason TEXT NOT NULL DEFAULT '',
 request_attempted INTEGER DEFAULT 0 CHECK(request_attempted IS NULL OR request_attempted IN (0,1)),
 http_status INTEGER, proxy_http_status INTEGER, stream_error_status INTEGER,
 error_code TEXT NOT NULL DEFAULT '', stream_error_code TEXT NOT NULL DEFAULT '', error_message TEXT NOT NULL DEFAULT '',
 failure_stage TEXT NOT NULL DEFAULT '', retry_after_seconds INTEGER, previous_check_id INTEGER,
 created_at INTEGER NOT NULL, started_at INTEGER, finished_at INTEGER, duration_ms INTEGER NOT NULL DEFAULT 0,
 UNIQUE(batch_id,account_id)
);
CREATE INDEX IF NOT EXISTS account_checks_batch_state ON account_checks(batch_id,state,id);
CREATE INDEX IF NOT EXISTS account_checks_account_latest ON account_checks(account_id,id DESC);
CREATE INDEX IF NOT EXISTS login_attempts_account_success_id ON login_attempts(account_id,status,id DESC);
`)
	if err != nil {
		return fmt.Errorf("migrate account checks: %w", err)
	}
	rows, err := s.db.Query(`PRAGMA table_info(account_check_batches)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !columns["destination_key"] {
		if _, err := s.db.Exec(`ALTER TABLE account_check_batches ADD COLUMN destination_key TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

const checkBatchSelect = `SELECT b.id,b.state,b.stop_reason,b.model,b.protocol_version,b.concurrency,b.proxy_mode,b.route_label,b.created_at,b.finished_at,b.destination_key,
 COUNT(c.id),COALESCE(SUM(c.state='queued'),0),COALESCE(SUM(c.state='running'),0),COALESCE(SUM(c.state='finished'),0),COALESCE(SUM(c.state='skipped'),0),COALESCE(SUM(c.state='canceled'),0),COALESCE(SUM(c.state='interrupted'),0),COALESCE(SUM(c.state='finished' AND c.outcome='ok'),0),COALESCE(SUM(c.state='finished' AND c.outcome!='ok'),0),COALESCE(SUM(c.failure_stage='precheck'),0),COALESCE(SUM(c.request_attempted=1),0)
 FROM account_check_batches b LEFT JOIN account_checks c ON c.batch_id=b.id`

func scanCheckBatch(row scanner) (AccountCheckBatch, error) {
	var b AccountCheckBatch
	var finished sql.NullInt64
	err := row.Scan(&b.ID, &b.State, &b.StopReason, &b.Model, &b.ProtocolVersion, &b.Concurrency, &b.ProxyMode, &b.RouteLabel, &b.CreatedAt, &finished, &b.DestinationKey, &b.Counts.Total, &b.Counts.Queued, &b.Counts.Running, &b.Counts.Finished, &b.Counts.Skipped, &b.Counts.Canceled, &b.Counts.Interrupted, &b.Counts.OK, &b.Counts.Abnormal, &b.Counts.Precheck, &b.Counts.Attempted)
	b.FinishedAt = nullableInt64(finished)
	b.Counts.Settled = b.Counts.Total - b.Counts.Queued - b.Counts.Running
	if errors.Is(err, sql.ErrNoRows) {
		return b, checkError("batch_not_found", "检测批次不存在")
	}
	return b, err
}
func readCheckBatch(ctx context.Context, tx *sql.Tx, id string) (AccountCheckBatch, error) {
	return scanCheckBatch(tx.QueryRowContext(ctx, checkBatchSelect+` WHERE b.id=? GROUP BY b.id`, id))
}

const checkSelect = `SELECT c.id,c.batch_id,c.account_id,c.state,c.credential_attempt_id,c.outcome,c.skip_reason,c.request_attempted,c.http_status,c.proxy_http_status,c.stream_error_status,c.error_code,c.stream_error_code,c.error_message,c.failure_stage,c.retry_after_seconds,c.previous_check_id,c.created_at,c.started_at,c.finished_at,c.duration_ms,
 COALESCE((SELECT MAX(la.id) FROM login_attempts la WHERE la.account_id=c.account_id AND la.status='success'),0),b.model,b.route_label
 FROM account_checks c JOIN account_check_batches b ON b.id=c.batch_id`

func nullableInt64(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}
func nullableInt(v sql.NullInt64) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int64)
	return &n
}
func scanCheck(row scanner) (AccountCheck, error) {
	var c AccountCheck
	var account, version, attempted, httpStatus, proxyStatus, streamStatus, retry, previous, started, finished sql.NullInt64
	var current int64
	err := row.Scan(&c.ID, &c.BatchID, &account, &c.State, &version, &c.Outcome, &c.SkipReason, &attempted, &httpStatus, &proxyStatus, &streamStatus, &c.ErrorCode, &c.StreamErrorCode, &c.Message, &c.FailureStage, &retry, &previous, &c.CreatedAt, &started, &finished, &c.DurationMS, &current, &c.Model, &c.RouteLabel)
	if err != nil {
		return c, err
	}
	c.AccountID = nullableInt64(account)
	c.CredentialAttemptID = nullableInt64(version)
	c.HTTPStatus = nullableInt(httpStatus)
	c.ProxyHTTPStatus = nullableInt(proxyStatus)
	c.StreamErrorStatus = nullableInt(streamStatus)
	c.RetryAfterSeconds = nullableInt(retry)
	c.PreviousCheckID = nullableInt64(previous)
	c.StartedAt = nullableInt64(started)
	c.FinishedAt = nullableInt64(finished)
	if attempted.Valid {
		value := attempted.Int64 != 0
		c.RequestAttempted = &value
	}
	c.Freshness = "stale"
	if !account.Valid {
		c.Freshness = "account_removed"
	} else if version.Valid && version.Int64 == current && (version.Int64 > 0 || c.Outcome == "credential_missing") {
		c.Freshness = "current"
	}
	return c, nil
}
func readChecks(ctx context.Context, tx *sql.Tx, where string, args ...any) ([]AccountCheck, error) {
	rows, err := tx.QueryContext(ctx, checkSelect+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AccountCheck, 0)
	for rows.Next() {
		c, e := scanCheck(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func normalizedCheckInput(input AccountCheckInput) ([]int64, string, error) {
	if len(input.RequestKey) < 1 || len(input.RequestKey) > 128 || strings.ContainsAny(input.RequestKey, " \r\n\t") || len(input.AccountIDs) < 1 || len(input.AccountIDs) > 100 || (input.Concurrency != 1 && input.Concurrency != 2) || (input.ProxyMode != "default" && input.ProxyMode != "direct") {
		return nil, "", checkError("invalid_check_request", "检测参数无效")
	}
	seen := make(map[int64]bool)
	ids := make([]int64, 0, len(input.AccountIDs))
	for _, id := range input.AccountIDs {
		if id <= 0 {
			return nil, "", checkError("invalid_account_ids", "账号 ID 必须为正整数")
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	encoded, _ := json.Marshal(struct {
		IDs         []int64
		Concurrency int
		ProxyMode   string
		Destination string
	}{ids, input.Concurrency, input.ProxyMode, strings.TrimSpace(input.DestinationKey)})
	hash := sha256.Sum256(encoded)
	return ids, hex.EncodeToString(hash[:]), nil
}

// LookupAccountCheckBatch resolves retries before validating today's proxy configuration.
// CreateAccountCheckBatch repeats this check inside its write transaction.
func (s *Store) LookupAccountCheckBatch(ctx context.Context, input AccountCheckInput) (AccountCheckBatch, bool, error) {
	_, hash, err := normalizedCheckInput(input)
	if err != nil {
		return AccountCheckBatch{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AccountCheckBatch{}, false, err
	}
	defer tx.Rollback()
	var id, existingHash string
	err = tx.QueryRowContext(ctx, `SELECT id,request_hash FROM account_check_batches WHERE request_key=?`, input.RequestKey).Scan(&id, &existingHash)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountCheckBatch{}, false, nil
	}
	if err != nil {
		return AccountCheckBatch{}, false, err
	}
	if existingHash != hash {
		return AccountCheckBatch{}, false, checkError("idempotency_conflict", "相同请求标识的参数不同")
	}
	batch, err := readCheckBatch(ctx, tx, id)
	return batch, true, err
}

func (s *Store) CreateAccountCheckBatch(ctx context.Context, input AccountCheckInput) (AccountCheckBatch, bool, error) {
	ids, hash, err := normalizedCheckInput(input)
	if err != nil {
		return AccountCheckBatch{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountCheckBatch{}, false, err
	}
	defer tx.Rollback()
	var existingID, existingHash string
	err = tx.QueryRowContext(ctx, `SELECT id,request_hash FROM account_check_batches WHERE request_key=?`, input.RequestKey).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != hash {
			return AccountCheckBatch{}, false, checkError("idempotency_conflict", "相同请求标识的参数不同")
		}
		b, e := readCheckBatch(ctx, tx, existingID)
		return b, true, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AccountCheckBatch{}, false, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM account_check_batches WHERE state IN ('active','stopping') LIMIT 1`).Scan(&existingID)
	if err == nil {
		return AccountCheckBatch{}, false, &AccountCheckError{Code: "batch_active", Message: "已有检测批次正在执行", ActiveBatchID: existingID}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AccountCheckBatch{}, false, err
	}
	invalid := make([]int64, 0)
	destination := strings.TrimSpace(input.DestinationKey)
	for _, id := range ids {
		var eligible bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM accounts a JOIN sub2_imports i ON i.account_id=a.id WHERE a.id=? AND i.state='imported' AND i.sub2_account_id>0 AND (?='' OR i.destination_key=?))`, id, destination, destination).Scan(&eligible); err != nil {
			return AccountCheckBatch{}, false, err
		}
		if !eligible {
			invalid = append(invalid, id)
		}
	}
	if len(invalid) > 0 {
		return AccountCheckBatch{}, false, &AccountCheckError{Code: "ineligible_accounts", Message: "部分账号不存在或没有已确认导入记录", InvalidAccountIDs: invalid}
	}
	route := "sys1 IPv4 直连"
	var cipher []byte
	if input.ProxyMode == "default" {
		if strings.TrimSpace(input.Proxy) == "" {
			return AccountCheckBatch{}, false, checkError("proxy_not_configured", "服务器默认代理未配置")
		}
		route = "服务器默认代理"
		cipher, err = s.seal(input.Proxy)
		if err != nil {
			return AccountCheckBatch{}, false, err
		}
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		return AccountCheckBatch{}, false, err
	}
	id := hex.EncodeToString(random)
	now := time.Now().UnixMilli()
	_, err = tx.ExecContext(ctx, `INSERT INTO account_check_batches(id,request_key,request_hash,state,model,protocol_version,concurrency,destination_key,proxy_mode,route_label,proxy_cipher,created_at) VALUES(?,?,?,'active',?,1,?,?,?,?,?,?)`, id, input.RequestKey, hash, AccountCheckModel, input.Concurrency, destination, input.ProxyMode, route, cipher, now)
	if err != nil {
		return AccountCheckBatch{}, false, err
	}
	for _, accountID := range ids {
		if _, err = tx.ExecContext(ctx, `INSERT INTO account_checks(batch_id,account_id,state,created_at) VALUES(?,?,'queued',?)`, id, accountID, now); err != nil {
			return AccountCheckBatch{}, false, err
		}
	}
	batch, err := readCheckBatch(ctx, tx, id)
	if err != nil {
		return AccountCheckBatch{}, false, err
	}
	return batch, false, tx.Commit()
}

// settleCheckBatch runs only after request owners have persisted their terminal state.
func settleCheckBatch(ctx context.Context, tx *sql.Tx, batchID string, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE account_check_batches SET state=CASE WHEN state='stopping' THEN 'stopped' ELSE 'completed' END,finished_at=?,proxy_cipher=NULL WHERE id=? AND state IN ('active','stopping') AND NOT EXISTS(SELECT 1 FROM account_checks WHERE batch_id=? AND state IN ('queued','running'))`, now, batchID, batchID)
	return err
}

// ClaimAccountCheck consumes local skips immediately, but never starts network work.
func (s *Store) ClaimAccountCheck(ctx context.Context) (*AccountCheckWork, error) {
	for range 100 {
		work, skipped, err := s.claimAccountCheck(ctx)
		if err != nil || !skipped {
			return work, err
		}
	}
	return nil, nil
}
func (s *Store) claimAccountCheck(ctx context.Context) (*AccountCheckWork, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var id int64
	var batchID string
	var accountID sql.NullInt64
	var destination string
	err = tx.QueryRowContext(ctx, `SELECT c.id,c.batch_id,c.account_id,b.destination_key FROM account_checks c JOIN account_check_batches b ON b.id=c.batch_id WHERE c.state='queued' AND b.state='active' AND (SELECT COUNT(*) FROM account_checks r WHERE r.batch_id=b.id AND r.state='running')<b.concurrency ORDER BY c.id LIMIT 1`).Scan(&id, &batchID, &accountID, &destination)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var accessCipher, proxyCipher []byte
	var chatGPTID, status string
	var version int64
	var eligible bool
	skip := ""
	var previous *int64
	if !accountID.Valid {
		skip = "account_removed"
	} else {
		err = tx.QueryRowContext(ctx, `SELECT a.access_token_cipher,COALESCE(a.chatgpt_account_id,''),a.status,COALESCE((SELECT MAX(id) FROM login_attempts WHERE account_id=a.id AND status='success'),0),EXISTS(SELECT 1 FROM sub2_imports i WHERE i.account_id=a.id AND i.state='imported' AND i.sub2_account_id>0 AND (?='' OR i.destination_key=?)) FROM accounts a WHERE a.id=?`, destination, destination, accountID.Int64).Scan(&accessCipher, &chatGPTID, &status, &version, &eligible)
		if err != nil {
			return nil, false, err
		}
		if !eligible {
			skip = "no_longer_imported"
		} else if status == "running" {
			skip = "login_in_progress"
		} else {
			var prior int64
			err = tx.QueryRowContext(ctx, `SELECT id FROM account_checks WHERE account_id=? AND state='finished' AND credential_attempt_id=? AND finished_at>? ORDER BY id DESC LIMIT 1`, accountID.Int64, version, time.Now().Add(-30*time.Second).UnixMilli()).Scan(&prior)
			if err == nil {
				skip = "cooldown"
				previous = &prior
			} else if !errors.Is(err, sql.ErrNoRows) {
				return nil, false, err
			}
		}
	}
	now := time.Now().UnixMilli()
	if skip != "" {
		_, err = tx.ExecContext(ctx, `UPDATE account_checks SET state='skipped',skip_reason=?,credential_attempt_id=?,previous_check_id=?,started_at=?,finished_at=? WHERE id=? AND state='queued'`, skip, version, previous, now, now, id)
		if err == nil {
			err = settleCheckBatch(ctx, tx, batchID, now)
		}
		if err != nil {
			return nil, false, err
		}
		return nil, true, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_checks SET state='running',credential_attempt_id=?,started_at=? WHERE id=? AND state='queued'`, version, now, id)
	if err != nil {
		return nil, false, err
	}
	batch, err := readCheckBatch(ctx, tx, batchID)
	if err != nil {
		return nil, false, err
	}
	item, err := scanCheck(tx.QueryRowContext(ctx, checkSelect+` WHERE c.id=?`, id))
	if err != nil {
		return nil, false, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT proxy_cipher FROM account_check_batches WHERE id=?`, batchID).Scan(&proxyCipher); err != nil {
		return nil, false, err
	}
	if err = tx.Commit(); err != nil {
		return nil, false, err
	}
	access, err := s.open(accessCipher)
	if err != nil {
		return nil, false, err
	}
	proxy, err := s.open(proxyCipher)
	if err != nil {
		return nil, false, err
	}
	if access == "" || version == 0 || chatGPTID == "" {
		result := AccountCheckResult{Outcome: "credential_incomplete", ErrorCode: "credential_incomplete", Message: "本地凭据信息不完整，请重新登录", FailureStage: "precheck"}
		if access == "" {
			result.Outcome = "credential_missing"
			result.ErrorCode = "credential_missing"
			result.Message = "缺少访问令牌，请重新登录"
		} else if version == 0 {
			result.ErrorCode = "credential_version_missing"
			result.Message = "缺少可靠凭据版本，请重新登录"
		}
		// The worker owns all terminal results, including local prechecks, so
		// recovery hooks observe missing credentials just like an upstream 401.
		return &AccountCheckWork{Batch: batch, Check: item, PrecheckResult: &result}, false, nil
	}
	return &AccountCheckWork{Batch: batch, Check: item, AccessToken: access, ChatGPTAccountID: chatGPTID, Proxy: proxy}, false, nil
}

func (s *Store) MarkAccountCheckAttempted(ctx context.Context, id int64) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE account_checks SET request_attempted=1 WHERE id=? AND state='running' AND EXISTS(SELECT 1 FROM account_check_batches b WHERE b.id=account_checks.batch_id AND b.state='active')`, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) FinishAccountCheck(ctx context.Context, id int64, result AccountCheckResult) (AccountCheckBatch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountCheckBatch{}, err
	}
	defer tx.Rollback()
	item, err := scanCheck(tx.QueryRowContext(ctx, checkSelect+` WHERE c.id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return AccountCheckBatch{}, checkError("check_not_found", "检测项不存在")
	}
	if err != nil {
		return AccountCheckBatch{}, err
	}
	batch, err := readCheckBatch(ctx, tx, item.BatchID)
	if err != nil {
		return batch, err
	}
	if item.State != "running" {
		return batch, nil
	}
	now := time.Now().UnixMilli()
	state := "finished"
	skip := ""
	if batch.State != "active" || result.Canceled {
		state = "canceled"
		result = AccountCheckResult{DurationMS: result.DurationMS, RequestAttempted: result.RequestAttempted}
	} else if item.AccountID == nil {
		state = "skipped"
		skip = "account_removed"
		result = AccountCheckResult{DurationMS: result.DurationMS, RequestAttempted: result.RequestAttempted}
	} else if result.Outcome == "" {
		return batch, checkError("invalid_check_result", "检测结束必须提供结果")
	}
	if result.DurationMS < 0 {
		result.DurationMS = 0
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_checks SET state=?,outcome=?,skip_reason=?,request_attempted=COALESCE(?,request_attempted),http_status=?,proxy_http_status=?,stream_error_status=?,error_code=?,stream_error_code=?,error_message=?,failure_stage=?,retry_after_seconds=?,finished_at=?,duration_ms=? WHERE id=? AND state='running'`, state, result.Outcome, skip, result.RequestAttempted, result.HTTPStatus, result.ProxyHTTPStatus, result.StreamErrorStatus, result.ErrorCode, result.StreamErrorCode, result.Message, result.FailureStage, result.RetryAfterSeconds, now, result.DurationMS, id)
	if err != nil {
		return batch, err
	}
	if batch.State == "active" && state == "finished" && result.Outcome == "proxy_error" && result.ProxyHTTPStatus != nil && *result.ProxyHTTPStatus == 407 {
		if _, err = tx.ExecContext(ctx, `UPDATE account_check_batches SET state='stopping',stop_reason='shared_proxy_failure' WHERE id=? AND state='active'`, batch.ID); err != nil {
			return batch, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE account_checks SET state='canceled',finished_at=? WHERE batch_id=? AND state='queued'`, now, batch.ID); err != nil {
			return batch, err
		}
	}
	if err = settleCheckBatch(ctx, tx, batch.ID, now); err != nil {
		return batch, err
	}
	batch, err = readCheckBatch(ctx, tx, batch.ID)
	if err != nil {
		return batch, err
	}
	return batch, tx.Commit()
}

func (s *Store) CancelAccountCheckBatch(ctx context.Context, id string) (AccountCheckBatch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountCheckBatch{}, err
	}
	defer tx.Rollback()
	batch, err := readCheckBatch(ctx, tx, id)
	if err != nil {
		return batch, err
	}
	if batch.State != "active" {
		return batch, nil
	}
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `UPDATE account_check_batches SET state='stopping',stop_reason='user_cancel' WHERE id=? AND state='active'`, id); err != nil {
		return batch, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_checks SET state='canceled',finished_at=? WHERE batch_id=? AND state='queued'`, now, id); err != nil {
		return batch, err
	}
	if err = settleCheckBatch(ctx, tx, id, now); err != nil {
		return batch, err
	}
	batch, err = readCheckBatch(ctx, tx, id)
	if err != nil {
		return batch, err
	}
	return batch, tx.Commit()
}

func (s *Store) GetAccountCheckBatch(ctx context.Context, id string) (AccountCheckBatch, []AccountCheck, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AccountCheckBatch{}, nil, err
	}
	defer tx.Rollback()
	batch, err := readCheckBatch(ctx, tx, id)
	if err != nil {
		return batch, nil, err
	}
	items, err := readChecks(ctx, tx, ` WHERE c.batch_id=? ORDER BY c.id`, id)
	return batch, items, err
}
func (s *Store) ListAccountCheckBatches(ctx context.Context, activeOnly bool, limit int) ([]AccountCheckBatch, error) {
	if limit < 1 || limit > 20 {
		limit = 20
	}
	where := ""
	if activeOnly {
		where = ` WHERE b.state IN ('active','stopping')`
	}
	rows, err := s.db.QueryContext(ctx, checkBatchSelect+where+` GROUP BY b.id ORDER BY b.created_at DESC,b.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	batches := make([]AccountCheckBatch, 0)
	for rows.Next() {
		b, e := scanCheckBatch(rows)
		if e != nil {
			return nil, e
		}
		batches = append(batches, b)
	}
	return batches, rows.Err()
}
func (s *Store) ListAccountChecks(ctx context.Context, accountID int64, limit int, destinations ...string) ([]AccountCheck, error) {
	if limit < 1 || limit > 20 {
		limit = 20
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	destination := ""
	if len(destinations) > 0 {
		destination = strings.TrimSpace(destinations[0])
	}
	return readChecks(ctx, tx, ` WHERE c.account_id=? AND (?='' OR b.destination_key=? OR (b.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=c.account_id AND legacy_dest.state='imported' AND legacy_dest.sub2_account_id>0)=1 AND EXISTS(SELECT 1 FROM sub2_imports legacy_match WHERE legacy_match.account_id=c.account_id AND legacy_match.destination_key=? AND legacy_match.state='imported' AND legacy_match.sub2_account_id>0))) ORDER BY c.id DESC LIMIT ?`, accountID, destination, destination, destination, limit)
}
func (s *Store) ListAccountCheckSummaries(ctx context.Context, destinations ...string) (map[int64]AccountCheckSummary, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	summaries := make(map[int64]AccountCheckSummary)
	destination := ""
	if len(destinations) > 0 {
		destination = strings.TrimSpace(destinations[0])
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.id,a.status,EXISTS(SELECT 1 FROM sub2_imports i WHERE i.account_id=a.id AND i.state='imported' AND i.sub2_account_id>0 AND (?='' OR i.destination_key=?)) FROM accounts a`, destination, destination)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var status string
		var imported bool
		if err = rows.Scan(&id, &status, &imported); err != nil {
			rows.Close()
			return nil, err
		}
		summary := AccountCheckSummary{AccountID: id, Eligible: imported}
		if !imported {
			summary.IneligibleReason = "not_imported"
		} else if status == "running" {
			summary.Eligible = false
			summary.IneligibleReason = "login_in_progress"
		}
		summaries[id] = summary
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	items, err := readChecks(ctx, tx, ` WHERE c.id IN (SELECT MAX(filtered.id) FROM account_checks filtered JOIN account_check_batches filtered_batch ON filtered_batch.id=filtered.batch_id WHERE filtered.account_id IS NOT NULL AND (?='' OR filtered_batch.destination_key=? OR (filtered_batch.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=filtered.account_id AND legacy_dest.state='imported' AND legacy_dest.sub2_account_id>0)=1 AND EXISTS(SELECT 1 FROM sub2_imports legacy_match WHERE legacy_match.account_id=filtered.account_id AND legacy_match.destination_key=? AND legacy_match.state='imported' AND legacy_match.sub2_account_id>0))) GROUP BY filtered.account_id) OR c.id IN (SELECT MAX(filtered.id) FROM account_checks filtered JOIN account_check_batches filtered_batch ON filtered_batch.id=filtered.batch_id WHERE filtered.account_id IS NOT NULL AND filtered.state='finished' AND (?='' OR filtered_batch.destination_key=? OR (filtered_batch.destination_key='' AND (SELECT COUNT(DISTINCT legacy_dest.destination_key) FROM sub2_imports legacy_dest WHERE legacy_dest.account_id=filtered.account_id AND legacy_dest.state='imported' AND legacy_dest.sub2_account_id>0)=1 AND EXISTS(SELECT 1 FROM sub2_imports legacy_match WHERE legacy_match.account_id=filtered.account_id AND legacy_match.destination_key=? AND legacy_match.state='imported' AND legacy_match.sub2_account_id>0))) GROUP BY filtered.account_id)`, destination, destination, destination, destination, destination, destination)
	if err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	for i := range items {
		item := &items[i]
		if item.AccountID == nil {
			continue
		}
		id := *item.AccountID
		summary := summaries[id]
		if summary.LatestTask == nil || item.ID > summary.LatestTask.ID {
			summary.LatestTask = item
		}
		if item.State == "finished" && (summary.LastResult == nil || item.ID > summary.LastResult.ID) {
			summary.LastResult = item
		}
		if item.State == "finished" && item.Freshness == "current" && item.FinishedAt != nil {
			remaining := *item.FinishedAt + 30000 - now
			if remaining > 0 {
				summary.CooldownRemainingSeconds = int((remaining + 999) / 1000)
			}
		}
		summaries[id] = summary
	}
	return summaries, nil
}

// RecoverAccountChecks is startup-only: no request owner may still be running.
func (s *Store) RecoverAccountChecks(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, `UPDATE account_checks SET state='interrupted',finished_at=?,duration_ms=CASE WHEN started_at IS NULL THEN 0 ELSE MAX(0,?-started_at) END WHERE state='running'`, now, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_checks SET state='canceled',finished_at=? WHERE state='queued' AND batch_id IN (SELECT id FROM account_check_batches WHERE state IN ('stopping','stopped'))`, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE account_check_batches SET state=CASE WHEN state='stopping' THEN 'stopped' ELSE 'completed' END,finished_at=?,proxy_cipher=NULL WHERE state IN ('active','stopping') AND NOT EXISTS(SELECT 1 FROM account_checks WHERE batch_id=account_check_batches.id AND state IN ('queued','running'))`, now); err != nil {
		return err
	}
	return tx.Commit()
}
