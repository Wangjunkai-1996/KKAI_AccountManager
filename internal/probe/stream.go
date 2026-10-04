package probe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

func readStream(ctx context.Context, body io.Reader) Result {
	limited := &io.LimitedReader{R: body, N: responseLimit + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), eventLimit+1)
	var data strings.Builder
	eventName := ""
	sawText := false
	consume := func() (Result, bool) {
		if data.Len() == 0 {
			eventName = ""
			return Result{}, false
		}
		result, terminal := consumeEvent(eventName, strings.TrimSuffix(data.String(), "\n"), &sawText)
		data.Reset()
		eventName = ""
		return result, terminal
	}
	for scanner.Scan() {
		if limited.N <= 0 {
			r := failure("protocol_error", "sse")
			r.ErrorCode = "response_too_large"
			return r
		}
		line := scanner.Text()
		if line == "" {
			if result, terminal := consume(); terminal {
				return result
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			if data.Len()+len(value)+1 > eventLimit {
				r := failure("protocol_error", "sse")
				r.ErrorCode = "response_too_large"
				return r
			}
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			r := failure("protocol_error", "sse")
			r.ErrorCode = "response_too_large"
			return r
		}
		r := transportFailure(ctx, err, false, 0)
		r.FailureStage = "sse"
		return r
	}
	if limited.N <= 0 {
		r := failure("protocol_error", "sse")
		r.ErrorCode = "response_too_large"
		return r
	}
	if result, terminal := consume(); terminal {
		return result
	}
	if ctx.Err() != nil {
		r := transportFailure(ctx, ctx.Err(), false, 0)
		r.FailureStage = "sse"
		return r
	}
	return failure("incomplete", "sse")
}

func consumeEvent(eventName, data string, sawText *bool) (Result, bool) {
	if data == "[DONE]" {
		return failure("incomplete", "sse"), true
	}
	var event struct {
		Type     string          `json:"type"`
		Delta    string          `json:"delta"`
		Error    json.RawMessage `json:"error"`
		Response struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
			Output []struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"output"`
		} `json:"response"`
	}
	if json.Unmarshal([]byte(data), &event) != nil {
		return failure("protocol_error", "sse"), true
	}
	if event.Type == "" {
		event.Type = eventName
	}
	if eventName != "" && event.Type != eventName {
		return failure("protocol_error", "sse"), true
	}
	hasError := func(raw json.RawMessage) bool { return len(raw) != 0 && string(raw) != "null" }
	if event.Type == "error" || event.Type == "response.failed" || hasError(event.Error) || hasError(event.Response.Error) {
		raw := []byte(data)
		if hasError(event.Response.Error) {
			raw = event.Response.Error
		}
		errInfo := extractError(raw)
		result := classifyError(errInfo)
		if result.Outcome == "" {
			result = failure("upstream_error", "sse")
			result.ErrorCode = "unknown_upstream_error"
		}
		result.FailureStage = "sse"
		result.StreamErrorCode = result.ErrorCode
		result.ErrorCode = ""
		for _, status := range []json.RawMessage{errInfo.StatusCode, errInfo.Status} {
			if n, err := strconv.ParseInt(string(status), 10, 64); err == nil && n >= 400 && n <= 599 {
				result.StreamErrorStatus = int(n)
				break
			}
		}
		if result.StreamErrorCode == "unknown_upstream_error" {
			switch result.StreamErrorStatus {
			case 401:
				result.Outcome = "unauthorized"
			case 403:
				result.Outcome = "forbidden"
			case 429:
				result.Outcome = "rate_limited"
			}
			result.Message = failure(result.Outcome, "").Message
		}
		return result, true
	}
	switch event.Type {
	case "response.output_text.delta":
		if strings.TrimSpace(event.Delta) != "" {
			*sawText = true
		}
	case "response.completed":
		if event.Response.Status != "completed" {
			return failure("incomplete", "sse"), true
		}
		for _, output := range event.Response.Output {
			for _, part := range output.Content {
				if part.Type == "output_text" && strings.TrimSpace(part.Text) != "" {
					*sawText = true
				}
			}
		}
		if !*sawText {
			return failure("incomplete", "sse"), true
		}
		return failure("ok", ""), true
	case "response.incomplete", "response.done":
		return failure("incomplete", "sse"), true
	}
	return Result{}, false
}
