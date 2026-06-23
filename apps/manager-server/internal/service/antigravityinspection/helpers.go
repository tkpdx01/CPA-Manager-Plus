package antigravityinspection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type runLogger struct {
	service *Service
	runID   int64
}

func (l runLogger) info(ctx context.Context, message string, detail any) { l.log(ctx, "info", message, detail) }
func (l runLogger) success(ctx context.Context, message string, detail any) {
	l.log(ctx, "success", message, detail)
}
func (l runLogger) warning(ctx context.Context, message string, detail any) {
	l.log(ctx, "warning", message, detail)
}
func (l runLogger) error(ctx context.Context, message string, detail any) { l.log(ctx, "error", message, detail) }

func (l runLogger) log(ctx context.Context, level string, message string, detail any) {
	if l.service == nil || l.runID <= 0 {
		return
	}
	_, _ = l.service.store.InsertAntigravityInspectionLog(ctx, model.AntigravityInspectionLog{
		RunID:   l.runID,
		Level:   level,
		Message: message,
		Detail:  sanitizeDetail(detail),
	})
}

func summarizeRun(run model.AntigravityInspectionRun, results []model.AntigravityInspectionResult) model.AntigravityInspectionRun {
	run.DisabledCount = 0
	run.EnabledCount = 0
	run.DeleteCount = 0
	run.DisableCount = 0
	run.EnableCount = 0
	run.ReauthCount = 0
	run.KeepCount = 0
	for _, result := range results {
		if result.Disabled {
			run.DisabledCount++
		} else {
			run.EnabledCount++
		}
		switch result.Action {
		case "delete":
			run.DeleteCount++
		case "disable":
			run.DisableCount++
		case "enable":
			run.EnableCount++
		case "reauth":
			run.ReauthCount++
		default:
			run.KeepCount++
		}
	}
	return run
}

func applyActionOutcomes(results []model.AntigravityInspectionResult, outcomes []ActionOutcome) []model.AntigravityInspectionResult {
	if len(outcomes) == 0 {
		return results
	}
	byKey := map[string]ActionOutcome{}
	for _, outcome := range outcomes {
		byKey[outcome.AccountKey] = outcome
	}
	out := make([]model.AntigravityInspectionResult, len(results))
	copy(out, results)
	for i := range out {
		outcome, ok := byKey[out[i].AccountKey]
		if !ok {
			continue
		}
		status := model.NormalizeAntigravityInspectionActionStatus(outcome.Status, out[i].Action)
		if status == model.AntigravityInspectionActionStatusPending {
			if outcome.Success {
				status = model.AntigravityInspectionActionStatusSuccess
			} else {
				status = model.AntigravityInspectionActionStatusFailed
			}
		}
		out[i].ActionStatus = status
		out[i].ActionError = outcome.Error
		out[i].ExecutedAction = ""
		if status == model.AntigravityInspectionActionStatusSuccess {
			out[i].ExecutedAction = outcome.Action
			out[i].ActionError = ""
			switch outcome.Action {
			case "disable":
				out[i].Disabled = true
			case "enable":
				out[i].Disabled = false
			}
		}
	}
	return out
}

func failedActionOutcome(item model.AntigravityInspectionResult, action string, message string) ActionOutcome {
	return ActionOutcome{
		ResultID:       item.ID,
		AccountKey:     item.AccountKey,
		FileName:       item.FileName,
		DisplayAccount: item.DisplayAccount,
		Action:         action,
		Status:         model.AntigravityInspectionActionStatusFailed,
		Success:        false,
		Error:          message,
	}
}

func needsReviewActionOutcome(item model.AntigravityInspectionResult, action string, message string) ActionOutcome {
	return ActionOutcome{
		ResultID:       item.ID,
		AccountKey:     item.AccountKey,
		FileName:       item.FileName,
		DisplayAccount: item.DisplayAccount,
		Action:         action,
		Status:         model.AntigravityInspectionActionStatusNeedsReview,
		Success:        true,
		Error:          message,
	}
}

func skippedActionOutcome(item model.AntigravityInspectionResult, action string, message string) ActionOutcome {
	return ActionOutcome{
		ResultID:       item.ID,
		AccountKey:     item.AccountKey,
		FileName:       item.FileName,
		DisplayAccount: item.DisplayAccount,
		Action:         action,
		Status:         model.AntigravityInspectionActionStatusSkipped,
		Success:        true,
		Error:          message,
	}
}

func countFailedOutcomes(outcomes []ActionOutcome) int {
	count := 0
	for _, outcome := range outcomes {
		if !outcome.Success {
			count++
		}
	}
	return count
}

func failedActionOutcomes(outcomes []ActionOutcome) []map[string]any {
	failed := make([]map[string]any, 0)
	for _, outcome := range outcomes {
		if outcome.Success {
			continue
		}
		failed = append(failed, map[string]any{
			"fileName":       outcome.FileName,
			"displayAccount": outcome.DisplayAccount,
			"action":         outcome.Action,
			"error":          outcome.Error,
		})
	}
	return failed
}

func formatResetTime(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		if t, err2 := time.Parse(time.RFC3339, value); err2 == nil {
			return t.Local().Format("01/02 15:04")
		}
		return "-"
	}
	return t.Local().Format("01/02 15:04")
}

func parseResetLabel(label string) (time.Time, bool) {
	label = strings.TrimSpace(label)
	if label == "" || label == "-" {
		return time.Time{}, false
	}
	now := time.Now()
	year := now.Year()
	full := fmt.Sprintf("%d %s", year, label)
	t, err := time.ParseInLocation("2006 01/02 15:04", full, time.Local)
	if err != nil {
		return time.Time{}, false
	}
	if t.Before(now) {
		t = t.AddDate(1, 0, 0)
	}
	return t, true
}

func readString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := record[key]
		if !ok || value == nil {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			return text
		}
	}
	return ""
}

func readMap(record map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		value, ok := record[key]
		if !ok || value == nil {
			continue
		}
		if typed, ok := value.(map[string]any); ok {
			return typed
		}
	}
	return nil
}

func readMapSlice(record map[string]any, keys ...string) []map[string]any {
	value, ok := firstValue(record, keys...)
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		items := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if m, ok := item.(map[string]any); ok {
				items = append(items, m)
			}
		}
		return items
	}
	return nil
}

func readNumberPtr(record map[string]any, keys ...string) (*float64, bool) {
	for _, key := range keys {
		value, ok := record[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return &typed, true
		case json.Number:
			if parsed, err := strconv.ParseFloat(typed.String(), 64); err == nil {
				return &parsed, true
			}
		case int:
			v := float64(typed)
			return &v, true
		case int64:
			v := float64(typed)
			return &v, true
		case string:
			parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
			if err == nil {
				return &parsed, true
			}
		}
	}
	return nil, false
}

func readBool(record map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, ok := record[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case bool:
			return typed
		case string:
			normalized := strings.ToLower(strings.TrimSpace(typed))
			return normalized == "true" || normalized == "1" || normalized == "yes" || normalized == "on"
		case float64:
			return typed != 0
		}
	}
	return false
}

func readFloat(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case string:
		if parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(typed, "%")), 64); err == nil {
			return parsed
		}
	}
	return fallback
}

func firstValue(record map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		value, ok := record[key]
		if ok {
			return value, true
		}
	}
	return nil, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeAuthIndex(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case float64:
		if math.Trunc(typed) == typed {
			return fmt.Sprintf("%.0f", typed)
		}
	case int:
		return fmt.Sprint(typed)
	case int64:
		return fmt.Sprint(typed)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func isDisabledAuthFile(file authFile) bool {
	status := strings.ToLower(firstNonEmpty(readString(file, "status"), readString(file, "state")))
	if status == "disabled" || status == "inactive" {
		return true
	}
	value, ok := file["disabled"]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case float64:
		return typed != 0
	case string:
		normalized := strings.ToLower(strings.TrimSpace(typed))
		return normalized == "true" || normalized == "1"
	default:
		return false
	}
}

func parseRecord(input any) map[string]any {
	switch typed := input.(type) {
	case map[string]any:
		return typed
	case string:
		var parsed map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(typed)), &parsed); err == nil {
			return parsed
		}
	}
	return nil
}

func normalizeBody(input any) (string, any) {
	if input == nil {
		return "", nil
	}
	if text, ok := input.(string); ok {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return text, nil
		}
		var parsed any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			return text, parsed
		}
		return text, text
	}
	data, err := json.Marshal(input)
	if err != nil {
		return fmt.Sprint(input), input
	}
	return string(data), input
}

func normalizeWindowID(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}
	var builder strings.Builder
	lastDash := false
	for _, char := range trimmed {
		isAlphaNumeric := (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')
		if isAlphaNumeric {
			builder.WriteRune(char)
			lastDash = false
			continue
		}
		if !lastDash && builder.Len() > 0 {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func clampFraction(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func containsAny(text string, tokens ...string) bool {
	for _, token := range tokens {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...(truncated)"
}

func sanitizeDetail(detail any) any {
	if detail == nil {
		return nil
	}
	data, err := json.Marshal(detail)
	if err != nil {
		return detail
	}
	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return detail
	}
	return redactValue(parsed)
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSecretKey(key) {
				result[key] = "[redacted]"
				continue
			}
			result[key] = redactValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, item := range typed {
			result[i] = redactValue(item)
		}
		return result
	default:
		return typed
	}
}

func isSecretKey(key string) bool {
	normalized := strings.ToLower(key)
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "key")
}

func padBase64(value string) string {
	switch len(value) % 4 {
	case 2:
		return value + "=="
	case 3:
		return value + "="
	default:
		return value
	}
}

func parseIDTokenPayload(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			return parsed
		}
		segments := strings.Split(trimmed, ".")
		if len(segments) < 2 {
			return nil
		}
		decoded, err := base64.RawURLEncoding.DecodeString(segments[1])
		if err != nil {
			decoded, err = base64.URLEncoding.DecodeString(padBase64(segments[1]))
			if err != nil {
				return nil
			}
		}
		if err := json.Unmarshal(decoded, &parsed); err == nil {
			return parsed
		}
	}
	return nil
}
