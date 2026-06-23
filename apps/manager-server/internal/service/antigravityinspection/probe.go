package antigravityinspection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const defaultAntigravityProjectID = "bamboo-precept-lgxtn"

var antigravityQuotaURLs = []string{
	"https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
	"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:fetchAvailableModels",
	"https://cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels",
}

func (s *Service) fetchAuthFiles(ctx context.Context, setup store.Setup) ([]authFile, error) {
	files, _, err := s.fetchAuthFilesAt(ctx, setup, "/v0/management/auth-files")
	return files, err
}

func (s *Service) fetchAuthFilesAt(ctx context.Context, setup store.Setup, path string) ([]authFile, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cpa.NormalizeBaseURL(setup.CPAUpstreamURL)+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+setup.ManagementKey)
	res, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, res.StatusCode, fmt.Errorf("auth files request failed: %s %s", res.Status, truncate(string(body), maxStoredBodyText))
	}
	var payload struct {
		Files []authFile `json:"files"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, res.StatusCode, err
	}
	return payload.Files, res.StatusCode, nil
}

func (s *Service) inspectSingleAccount(ctx context.Context, setup store.Setup, settings model.ManagerAntigravityInspectionConfig, item account, logger runLogger) model.AntigravityInspectionResult {
	base := resultFromAccount(item)
	if item.AuthIndex == "" {
		base.Action = "keep"
		base.ActionReason = "缺少 auth_index，保留账号"
		base.Error = "缺少 auth_index"
		base.ErrorKind = "missing_auth_index"
		base.ErrorDetail = "缺少 auth_index"
		logger.warning(ctx, "Antigravity 账号缺少 auth_index，跳过探测", map[string]any{"fileName": item.FileName, "displayAccount": item.DisplayAccount})
		return base
	}

	var response apiCallResponse
	var err error
	for attempt := 0; attempt <= settings.Retries; attempt++ {
		response, err = s.requestAntigravityQuota(ctx, setup, settings, item)
		if err == nil {
			break
		}
	}
	if err != nil {
		base.Action = "keep"
		base.ActionReason = "探测异常，保留账号"
		base.Error = truncate(err.Error(), maxStoredBodyText)
		base.ErrorKind = "request_error"
		base.ErrorDetail = truncate(err.Error(), maxStoredBodyText)
		logger.warning(ctx, "Antigravity 账号探测异常，保留账号", map[string]any{"fileName": item.FileName, "displayAccount": item.DisplayAccount, "error": err.Error()})
		return base
	}
	if !response.HasStatusCode {
		base.Action = "keep"
		base.ActionReason = "探测响应缺少 status_code，保留账号"
		base.Error = "响应缺少 status_code"
		base.ErrorKind = "missing_status"
		base.ErrorDetail = firstNonEmpty(truncate(response.BodyText, maxStoredBodyText), "响应缺少 status_code")
		logger.warning(ctx, "Antigravity 账号探测未返回 status_code，保留账号", map[string]any{"fileName": item.FileName, "displayAccount": item.DisplayAccount, "body": truncate(response.BodyText, maxStoredBodyText)})
		return base
	}

	statusCode := response.StatusCode
	base.StatusCode = &statusCode
	payload := parseRecord(response.Body)
	if payload == nil {
		payload = parseRecord(response.BodyText)
	}
	windows := buildAntigravityInspectionQuotaWindows(payload)
	usedPercent := deriveAntigravityUsedPercent(windows)
	decision := resolveProbeAction(item, statusCode, response.BodyText, windows, usedPercent, settings.UsedPercentThreshold)
	base.Action = decision.Action
	base.ActionReason = decision.ActionReason
	base.UsedPercent = decision.UsedPercent
	base.IsQuota = decision.IsQuota
	base.RecoverAtMS = decision.RecoverAtMS
	base.RecoverLabel = decision.RecoverLabel
	base.QuotaWindows = windows
	base.Error = ""
	base.ErrorKind = decision.ErrorKind
	if statusCode < 200 || statusCode >= 300 {
		base.ErrorDetail = firstNonEmpty(truncate(response.BodyText, maxStoredBodyText), fmt.Sprintf("HTTP %d", statusCode))
	}
	level := "info"
	switch decision.Action {
	case "delete":
		level = "error"
	case "disable":
		level = "warning"
	case "enable":
		level = "success"
	}
	logger.log(ctx, level, "Antigravity 账号探测完成", map[string]any{"fileName": item.FileName, "displayAccount": item.DisplayAccount, "action": decision.Action, "statusCode": statusCode, "usedPercent": nullableFloat(decision.UsedPercent), "recoverAtMs": decision.RecoverAtMS})
	return base
}

func (s *Service) requestAntigravityQuota(ctx context.Context, setup store.Setup, settings model.ManagerAntigravityInspectionConfig, item account) (apiCallResponse, error) {
	var last apiCallResponse
	var lastErr error
	priorityStatus := 0
	priorityResponse := apiCallResponse{}
	for _, targetURL := range antigravityQuotaURLs {
		response, err := s.requestAntigravityQuotaURL(ctx, setup, settings, item, targetURL)
		if err != nil {
			lastErr = err
			continue
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return response, nil
		}
		last = response
		if response.StatusCode == http.StatusUnauthorized {
			return response, nil
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusTooManyRequests {
			if priorityStatus == 0 || response.StatusCode == http.StatusTooManyRequests {
				priorityStatus = response.StatusCode
				priorityResponse = response
			}
		}
	}
	if priorityStatus != 0 {
		return priorityResponse, nil
	}
	if last.HasStatusCode {
		return last, nil
	}
	if lastErr != nil {
		return apiCallResponse{}, lastErr
	}
	return apiCallResponse{}, fmt.Errorf("antigravity quota request failed")
}

func (s *Service) requestAntigravityQuotaURL(ctx context.Context, setup store.Setup, settings model.ManagerAntigravityInspectionConfig, item account, targetURL string) (apiCallResponse, error) {
	body := map[string]any{"project": resolveAntigravityProjectID(item.File)}
	bodyData, err := json.Marshal(body)
	if err != nil {
		return apiCallResponse{}, err
	}
	payload := map[string]any{
		"authIndex": item.AuthIndex,
		"method":    http.MethodPost,
		"url":       targetURL,
		"header": map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Content-Type":  "application/json",
			"User-Agent":    settings.UserAgent,
		},
		"data": string(bodyData),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return apiCallResponse{}, err
	}
	requestCtx := ctx
	cancel := func() {}
	if settings.Timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, time.Duration(settings.Timeout)*time.Millisecond)
	}
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, cpa.NormalizeBaseURL(setup.CPAUpstreamURL)+"/v0/management/api-call", bytes.NewReader(data))
	if err != nil {
		return apiCallResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+setup.ManagementKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := s.client.Do(req)
	if err != nil {
		return apiCallResponse{}, err
	}
	defer res.Body.Close()
	resBody, _ := io.ReadAll(io.LimitReader(res.Body, 8*1024*1024))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return apiCallResponse{}, fmt.Errorf("api-call failed: %s %s", res.Status, truncate(string(resBody), maxStoredBodyText))
	}
	var raw map[string]any
	if err := json.Unmarshal(resBody, &raw); err != nil {
		return apiCallResponse{}, err
	}
	statusRaw, hasStatus := firstValue(raw, "status_code", "statusCode")
	statusCode := int(readFloat(statusRaw, 0))
	bodyRaw, _ := firstValue(raw, "body")
	bodyText, bodyValue := normalizeBody(bodyRaw)
	return apiCallResponse{StatusCode: statusCode, HasStatusCode: hasStatus && strings.TrimSpace(fmt.Sprint(statusRaw)) != "", BodyText: bodyText, Body: bodyValue}, nil
}

func resultFromAccount(item account) model.AntigravityInspectionResult {
	return model.AntigravityInspectionResult{AccountKey: item.Key, FileName: item.FileName, DisplayAccount: item.DisplayAccount, AuthIndex: item.AuthIndex, AccountID: item.AccountID, Provider: item.Provider, Disabled: item.Disabled, Status: item.Status, State: item.State, Action: "keep", ActionReason: "无需处理", IsQuota: false}
}

func toAccount(file authFile) account {
	fileName := firstNonEmpty(readString(file, "name"), readString(file, "id"), normalizeAuthIndex(file["auth_index"]), normalizeAuthIndex(file["authIndex"]), "unknown-auth-file")
	authIndex := firstNonEmpty(normalizeAuthIndex(file["auth_index"]), normalizeAuthIndex(file["authIndex"]), normalizeAuthIndex(file["auth-index"]))
	provider := strings.ToLower(firstNonEmpty(readString(file, "provider"), readString(file, "type")))
	projectID := resolveAntigravityProjectID(file)
	displayAccount := firstNonEmpty(readString(file, "account"), readString(file, "email"), readString(file, "label"), projectID, fileName)
	key := fileName + "::" + authIndex
	if authIndex == "" {
		key = fileName + "::-"
	}
	return account{Key: key, FileName: fileName, DisplayAccount: displayAccount, AuthIndex: authIndex, AccountID: projectID, Provider: provider, Disabled: isDisabledAuthFile(file), Status: readString(file, "status"), State: readString(file, "state"), File: file}
}

func resolveAntigravityProjectID(file authFile) string {
	metadata := readMap(file, "metadata")
	attributes := readMap(file, "attributes")
	return firstNonEmpty(
		readString(file, "project_id", "projectId"),
		readString(metadata, "project_id", "projectId"),
		readString(attributes, "project_id", "projectId", "gemini_virtual_project", "geminiVirtualProject"),
		defaultAntigravityProjectID,
	)
}

func buildAntigravityInspectionQuotaWindows(payload map[string]any) []model.CodexInspectionQuotaWindow {
	if payload == nil {
		return nil
	}
	windows := make([]model.CodexInspectionQuotaWindow, 0)
	groups := readMapSlice(payload, "groups")
	for groupIndex, group := range groups {
		groupLabel := firstNonEmpty(readString(group, "displayName", "display_name"), fmt.Sprintf("Quota Group %d", groupIndex+1))
		groupID := normalizeWindowID(groupLabel)
		if groupID == "" {
			groupID = fmt.Sprintf("quota-group-%d", groupIndex+1)
		}
		for bucketIndex, bucket := range readMapSlice(group, "buckets") {
			remaining, ok := readNumberPtr(bucket, "remainingFraction", "remaining_fraction")
			if !ok || remaining == nil {
				continue
			}
			used := clampPercent((1 - clampFraction(*remaining)) * 100)
			label := firstNonEmpty(readString(bucket, "displayName", "display_name"), readString(bucket, "bucketId", "bucket_id"), fmt.Sprintf("%s-%d", groupID, bucketIndex+1))
			windows = append(windows, model.CodexInspectionQuotaWindow{ID: fmt.Sprintf("%s-%d", groupID, bucketIndex), LabelKey: "antigravity_quota.window", LabelParams: map[string]any{"name": groupLabel, "bucket": label}, UsedPercent: &used, ResetLabel: formatResetTime(readString(bucket, "resetTime", "reset_time"))})
		}
	}
	if len(windows) > 0 {
		return windows
	}
	models := readMap(payload, "models")
	keys := make([]string, 0, len(models))
	for key := range models {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry, ok := models[key].(map[string]any)
		if !ok {
			continue
		}
		quotaInfo := readMap(entry, "quotaInfo", "quota_info")
		remaining, ok := readNumberPtr(quotaInfo, "remainingFraction", "remaining_fraction", "remaining")
		if !ok || remaining == nil {
			continue
		}
		used := clampPercent((1 - clampFraction(*remaining)) * 100)
		label := firstNonEmpty(readString(entry, "displayName", "display_name"), key)
		windows = append(windows, model.CodexInspectionQuotaWindow{ID: normalizeWindowID(key), LabelKey: "antigravity_quota.model_window", LabelParams: map[string]any{"name": label}, UsedPercent: &used, ResetLabel: formatResetTime(readString(quotaInfo, "resetTime", "reset_time"))})
	}
	return windows
}

func deriveAntigravityUsedPercent(windows []model.CodexInspectionQuotaWindow) *float64 {
	var values []float64
	for _, window := range windows {
		if window.UsedPercent != nil {
			values = append(values, *window.UsedPercent)
		}
	}
	if len(values) == 0 {
		return nil
	}
	max := values[0]
	for _, value := range values[1:] {
		if value > max {
			max = value
		}
	}
	return &max
}

func earliestRecoverAt(windows []model.CodexInspectionQuotaWindow) (int64, string) {
	var chosen time.Time
	var label string
	for _, window := range windows {
		if window.ResetLabel == "" || window.ResetLabel == "-" {
			continue
		}
		parsed, ok := parseResetLabel(window.ResetLabel)
		if !ok || !parsed.After(time.Now()) {
			continue
		}
		if chosen.IsZero() || parsed.Before(chosen) {
			chosen = parsed
			label = window.ResetLabel
		}
	}
	if chosen.IsZero() {
		return 0, ""
	}
	return chosen.UnixMilli(), label
}

func resolveProbeAction(item account, statusCode int, bodyText string, windows []model.CodexInspectionQuotaWindow, usedPercent *float64, threshold float64) inspectionDecision {
	bodyLower := strings.ToLower(bodyText)
	if statusCode == http.StatusUnauthorized || containsAny(bodyLower, "invalid_grant", "token expired", "token has expired", "unauthorized", "invalid token") {
		return inspectionDecision{Action: "delete", ActionReason: "接口返回认证失效，确认死透，建议删除账号", UsedPercent: usedPercent, ErrorKind: "unauthorized"}
	}
	if statusCode == http.StatusForbidden || statusCode == http.StatusNotFound || containsAny(bodyLower, "permission denied", "project not found", "not found") {
		return inspectionDecision{Action: "delete", ActionReason: fmt.Sprintf("接口返回 %d，账号或项目不可用，建议删除账号", statusCode), UsedPercent: usedPercent, ErrorKind: "dead_account"}
	}
	recoverAt, recoverLabel := earliestRecoverAt(windows)
	isRateLimited := statusCode == http.StatusTooManyRequests || containsAny(bodyLower, "rate limit", "quota", "resource exhausted", "limit exceeded")
	overThreshold := usedPercent != nil && *usedPercent >= threshold
	if isRateLimited || overThreshold {
		if recoverAt <= 0 {
			return inspectionDecision{Action: "keep", ActionReason: "额度或限流已触发，但缺少恢复时间，保留账号等待人工确认", UsedPercent: usedPercent, IsQuota: true, ErrorKind: "rate_limit"}
		}
		if item.Disabled {
			return inspectionDecision{Action: "keep", ActionReason: "额度或限流已触发，但账号已禁用并等待恢复", UsedPercent: usedPercent, IsQuota: true, RecoverAtMS: recoverAt, RecoverLabel: recoverLabel, ErrorKind: "rate_limit"}
		}
		return inspectionDecision{Action: "disable", ActionReason: "额度或限流已触发，建议禁用账号并在恢复时间自动启用", UsedPercent: usedPercent, IsQuota: true, RecoverAtMS: recoverAt, RecoverLabel: recoverLabel, ErrorKind: "rate_limit"}
	}
	if statusCode >= 200 && statusCode < 300 && item.Disabled {
		return inspectionDecision{Action: "enable", ActionReason: "账号恢复健康，建议重新启用", UsedPercent: usedPercent}
	}
	return inspectionDecision{Action: "keep", ActionReason: "无需处理", UsedPercent: usedPercent}
}
