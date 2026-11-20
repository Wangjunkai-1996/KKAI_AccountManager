package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

// Shares the management gateway and same-origin middleware with history.
// A custom header plus JSON excludes cross-site form submissions.
func handleHistoryCredentialReveal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		respondJSONStatus(w, http.StatusMethodNotAllowed, LoginResponse{Code: "method_not_allowed", Message: "Method not allowed"})
		return
	}
	if r.Header.Get("X-Requested-With") != "XMLHttpRequest" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		respondJSONStatus(w, http.StatusForbidden, LoginResponse{Code: "request_forbidden", Message: "请求来源无效"})
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		respondJSONStatus(w, http.StatusUnsupportedMediaType, LoginResponse{Code: "invalid_content_type", Message: "请求必须使用 application/json"})
		return
	}
	id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/history/"), "/credentials/reveal"), 10, 64)
	if err != nil || id <= 0 {
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Code: "invalid_account_id", Message: "账号 ID 无效"})
		return
	}
	var req struct {
		Field string `json:"field"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || (req.Field != "password" && req.Field != "totp_secret") {
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Code: "invalid_credential_field", Message: "只能读取密码或 TOTP 密钥中的一个字段"})
		return
	}
	if loginHistory == nil {
		respondJSONStatus(w, http.StatusServiceUnavailable, LoginResponse{Code: "history_unavailable", Message: "历史记录未初始化"})
		return
	}
	value, err := loginHistory.RevealCredential(r.Context(), id, req.Field)
	log.Printf("credential reveal account_id=%d field=%s success=%t", id, req.Field, err == nil && value != "")
	switch {
	case errors.Is(err, store.ErrAccountNotFound):
		respondJSONStatus(w, http.StatusNotFound, LoginResponse{Code: "account_not_found", Message: "账号不存在"})
	case errors.Is(err, store.ErrAccountDiscarded):
		respondJSONStatus(w, http.StatusGone, LoginResponse{Code: "account_discarded", Message: "账号已在本地退役"})
	case errors.Is(err, store.ErrAccountOfficialUnavailable):
		respondJSONStatus(w, http.StatusGone, LoginResponse{Code: "account_official_unavailable", Message: "官方已停用或删除账号"})
	case err != nil:
		respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Code: "credential_read_failed", Message: "读取登录资料失败"})
	case value == "":
		respondJSONStatus(w, http.StatusNotFound, LoginResponse{Code: "credential_missing", Message: "该字段未保存或已清除"})
	default:
		respondJSON(w, struct {
			Success   bool   `json:"success"`
			AccountID int64  `json:"account_id"`
			Field     string `json:"field"`
			Value     string `json:"value"`
		}{true, id, req.Field, value})
	}
}
