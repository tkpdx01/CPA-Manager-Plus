package antigravityinspection

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/cpa"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

type fileActionGroup struct {
	FileName string
	Items    []model.AntigravityInspectionResult
	Action   string
	Mixed    bool
}

const (
	fileActionDuplicateReason = "CPA 认证文件动作按文件执行，该文件已由另一条结果处理"
	fileActionMixedReason     = "同一认证文件下存在多个不同建议动作，文件级处理已阻止，请到认证文件管理中手动处理"
)

func (s *Service) executeAutoActions(ctx context.Context, setup store.Setup, settings model.ManagerAntigravityInspectionConfig, results []model.AntigravityInspectionResult, logger runLogger) []ActionOutcome {
	mode := model.NormalizeAntigravityInspectionAutoActionMode(settings.AutoActionMode, model.AntigravityInspectionAutoActionNone)
	items, preflightOutcomes := selectAutoActionItems(mode, results)
	if len(items) == 0 {
		return preflightOutcomes
	}
	outcomes := make([]ActionOutcome, 0, len(preflightOutcomes)+len(items))
	outcomes = append(outcomes, preflightOutcomes...)
	outcomes = append(outcomes, s.executeActionItems(ctx, setup, settings, items, logger, "自动处理", func(item model.AntigravityInspectionResult) string {
		return resolveExecutableAction(mode, item.Action)
	})...)
	return outcomes
}

func (s *Service) executeActionItems(ctx context.Context, setup store.Setup, settings model.ManagerAntigravityInspectionConfig, items []model.AntigravityInspectionResult, logger runLogger, logPrefix string, actionFor func(model.AntigravityInspectionResult) string) []ActionOutcome {
	workers := settings.DeleteWorkers
	if workers <= 0 {
		workers = 1
	}
	jobs := make(chan model.AntigravityInspectionResult)
	outcomes := make(chan ActionOutcome, len(items))
	var wg sync.WaitGroup
	for i := 0; i < workers && i < len(items); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-jobs:
					if !ok {
						return
					}
					action := item.Action
					if actionFor != nil {
						action = actionFor(item)
					}
					actionItem := item
					actionItem.Action = action
					outcome := ActionOutcome{ResultID: item.ID, AccountKey: item.AccountKey, FileName: item.FileName, DisplayAccount: item.DisplayAccount, Action: action}
					if err := s.executeAction(ctx, setup, settings, actionItem); err != nil {
						outcome.Success = false
						outcome.Status = model.AntigravityInspectionActionStatusFailed
						outcome.Error = err.Error()
						outcomes <- outcome
						logger.error(ctx, logPrefix+" Antigravity 账号失败", map[string]any{"fileName": item.FileName, "displayAccount": item.DisplayAccount, "action": action, "error": err.Error()})
						continue
					}
					outcome.Success = true
					outcome.Status = model.AntigravityInspectionActionStatusSuccess
					outcomes <- outcome
					logger.success(ctx, logPrefix+" Antigravity 账号成功", map[string]any{"fileName": item.FileName, "displayAccount": item.DisplayAccount, "action": action})
				}
			}
		}()
	}
	for _, item := range items {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			close(outcomes)
			return collectActionOutcomes(outcomes, len(items))
		case jobs <- item:
		}
	}
	close(jobs)
	wg.Wait()
	close(outcomes)
	return collectActionOutcomes(outcomes, len(items))
}

func collectActionOutcomes(outcomes <-chan ActionOutcome, capacity int) []ActionOutcome {
	result := make([]ActionOutcome, 0, capacity)
	for outcome := range outcomes {
		result = append(result, outcome)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].FileName == result[j].FileName {
			return result[i].Action < result[j].Action
		}
		return result[i].FileName < result[j].FileName
	})
	return result
}

func (s *Service) executeAction(ctx context.Context, setup store.Setup, settings model.ManagerAntigravityInspectionConfig, item model.AntigravityInspectionResult) error {
	switch item.Action {
	case "delete":
		return s.deleteAuthFileOnly(ctx, setup, "/v0/management/auth-files", item.FileName)
	case "disable":
		if item.RecoverAtMS > 0 && settings.RateLimitAction != model.AntigravityInspectionRateLimitActionNone {
			if err := s.patchAuthFileStatus(ctx, setup, item.FileName, true); err != nil {
				return err
			}
			_, err := s.store.UpsertQuotaCooldown(ctx, store.QuotaCooldownUpsert{AuthFileName: item.FileName, AuthIndex: item.AuthIndex, AccountSnapshot: item.DisplayAccount, Provider: item.Provider, RecoverAtMS: item.RecoverAtMS, Owner: model.QuotaCooldownOwnerAntigravityInspection, PreDisabledState: false})
			return err
		}
		return s.patchAuthFileStatus(ctx, setup, item.FileName, true)
	case "enable":
		return s.patchAuthFileStatus(ctx, setup, item.FileName, false)
	default:
		return nil
	}
}

func (s *Service) deleteAuthFileOnly(ctx context.Context, setup store.Setup, path string, fileName string) error {
	endpoint := cpa.NormalizeBaseURL(setup.CPAUpstreamURL) + path + "?name=" + url.QueryEscape(fileName)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+setup.ManagementKey)
	return s.doCPAAction(req)
}

func (s *Service) patchAuthFileStatus(ctx context.Context, setup store.Setup, fileName string, disabled bool) error {
	payload := map[string]any{"name": fileName, "disabled": disabled}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, cpa.NormalizeBaseURL(setup.CPAUpstreamURL)+"/v0/management/auth-files/status", strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+setup.ManagementKey)
	req.Header.Set("Content-Type", "application/json")
	return s.doCPAAction(req)
}

func (s *Service) doCPAAction(req *http.Request) error {
	res, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%s", res.Status)
	}
	return nil
}

func resolveExecutableAction(mode string, action string) string {
	if mode == model.AntigravityInspectionAutoActionDisable && action == "delete" {
		return "disable"
	}
	return action
}

func selectAutoActionItems(mode string, results []model.AntigravityInspectionResult) ([]model.AntigravityInspectionResult, []ActionOutcome) {
	mode = model.NormalizeAntigravityInspectionAutoActionMode(mode, model.AntigravityInspectionAutoActionNone)
	if mode == model.AntigravityInspectionAutoActionNone {
		return nil, nil
	}
	items := make([]model.AntigravityInspectionResult, 0)
	outcomes := make([]ActionOutcome, 0)
	for _, group := range buildExecutableFileActionGroups(results) {
		if group.Mixed {
			for _, result := range group.Items {
				outcomes = append(outcomes, needsReviewActionOutcome(result, result.Action, fileActionMixedReason))
			}
			continue
		}
		if len(group.Items) == 0 || !allowAutoAction(mode, group.Items[0]) {
			continue
		}
		items = append(items, group.Items[0])
		for _, result := range group.Items[1:] {
			outcomes = append(outcomes, skippedActionOutcome(result, result.Action, fileActionDuplicateReason))
		}
	}
	return items, outcomes
}

func buildExecutableFileActionGroups(results []model.AntigravityInspectionResult) []fileActionGroup {
	groupOrder := make([]string, 0)
	groupsByFileName := map[string]*fileActionGroup{}
	for _, result := range results {
		if !isExecutableInspectionAction(result.Action) {
			continue
		}
		fileName := strings.TrimSpace(result.FileName)
		if fileName == "" {
			continue
		}
		group, ok := groupsByFileName[fileName]
		if !ok {
			group = &fileActionGroup{FileName: fileName, Action: result.Action}
			groupsByFileName[fileName] = group
			groupOrder = append(groupOrder, fileName)
		}
		if result.Action != group.Action {
			group.Mixed = true
		}
		group.Items = append(group.Items, result)
	}
	groups := make([]fileActionGroup, 0, len(groupOrder))
	for _, fileName := range groupOrder {
		groups = append(groups, *groupsByFileName[fileName])
	}
	return groups
}

func allowAutoAction(mode string, result model.AntigravityInspectionResult) bool {
	switch mode {
	case model.AntigravityInspectionAutoActionEnable:
		return result.Action == "enable"
	case model.AntigravityInspectionAutoActionDisable:
		return result.Action == "enable" || result.Action == "disable" || result.Action == "delete"
	case model.AntigravityInspectionAutoActionDelete:
		return result.Action == "enable" || result.Action == "disable" || result.Action == "delete"
	default:
		return false
	}
}

func selectManualActionItems(results []model.AntigravityInspectionResult, selected map[int64]struct{}) ([]model.AntigravityInspectionResult, []ActionOutcome) {
	items := make([]model.AntigravityInspectionResult, 0, len(selected))
	outcomes := make([]ActionOutcome, 0)
	seenFileNames := map[string]struct{}{}
	for _, result := range results {
		if _, ok := selected[result.ID]; !ok {
			continue
		}
		if !isExecutableInspectionAction(result.Action) {
			outcomes = append(outcomes, skippedActionOutcome(result, result.Action, "该巡检结果不是可执行动作"))
			continue
		}
		fileName := strings.TrimSpace(result.FileName)
		if fileName == "" {
			outcomes = append(outcomes, failedActionOutcome(result, result.Action, "认证文件名为空，无法执行"))
			continue
		}
		if _, ok := seenFileNames[fileName]; ok {
			outcomes = append(outcomes, skippedActionOutcome(result, result.Action, "CPA 认证文件动作按文件执行，同名文件已由另一条结果处理"))
			continue
		}
		seenFileNames[fileName] = struct{}{}
		items = append(items, result)
	}
	return items, outcomes
}

func isExecutableInspectionAction(action string) bool {
	return action == "delete" || action == "disable" || action == "enable"
}
