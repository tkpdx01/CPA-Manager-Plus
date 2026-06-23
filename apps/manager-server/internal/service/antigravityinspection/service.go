package antigravityinspection

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

const maxStoredBodyText = 2048

var (
	ErrRunAlreadyActive    = errors.New("antigravity inspection is already running")
	ErrNotConfigured       = errors.New("usage service is not configured")
	ErrRunNotFound         = errors.New("antigravity inspection run not found")
	ErrRunNotCompleted     = errors.New("antigravity inspection run is not completed")
	ErrActionIDsRequired   = errors.New("antigravity inspection action result ids are required")
	ErrNoActionableResults = errors.New("antigravity inspection has no actionable results")
)

type Service struct {
	store                *store.Store
	managerConfigService *managerconfig.Service
	client               *http.Client

	mu      sync.Mutex
	running bool
}

type RunRequest struct {
	TriggerType string
	TriggerKey  string
}

type RunDetail struct {
	Run     model.AntigravityInspectionRun      `json:"run"`
	Results []model.AntigravityInspectionResult `json:"results"`
	Logs    []model.AntigravityInspectionLog    `json:"logs"`
}

type ExecuteActionsRequest struct {
	ResultIDs []int64 `json:"resultIds"`
}

type ActionOutcome struct {
	ResultID       int64  `json:"resultId,omitempty"`
	AccountKey     string `json:"accountKey,omitempty"`
	FileName       string `json:"fileName"`
	DisplayAccount string `json:"displayAccount"`
	Action         string `json:"action"`
	Status         string `json:"status"`
	Success        bool   `json:"success"`
	Error          string `json:"error,omitempty"`
}

type ExecuteActionsResult struct {
	Outcomes []ActionOutcome `json:"outcomes"`
	Detail   RunDetail       `json:"detail"`
}

type authFile map[string]any

type account struct {
	Key            string
	FileName       string
	DisplayAccount string
	AuthIndex      string
	AccountID      string
	Provider       string
	Disabled       bool
	Status         string
	State          string
	File           authFile
}

type apiCallResponse struct {
	StatusCode    int
	HasStatusCode bool
	BodyText      string
	Body          any
}

type inspectionDecision struct {
	Action       string
	ActionReason string
	UsedPercent  *float64
	IsQuota      bool
	RecoverAtMS  int64
	RecoverLabel string
	ErrorKind    string
}

func New(st *store.Store, managerConfigService *managerconfig.Service, clients ...*http.Client) *Service {
	client := &http.Client{Timeout: 30 * time.Second}
	if len(clients) > 0 && clients[0] != nil {
		client = clients[0]
	}
	return &Service{store: st, managerConfigService: managerConfigService, client: client}
}

func (s *Service) Run(ctx context.Context, req RunRequest) (RunDetail, error) {
	if err := s.acquireRun(); err != nil {
		return RunDetail{}, err
	}
	defer s.releaseRun()

	settings, setup, err := s.resolveRuntime(ctx)
	if err != nil {
		return RunDetail{}, err
	}

	triggerType := strings.TrimSpace(req.TriggerType)
	if triggerType == "" {
		triggerType = model.AntigravityInspectionTriggerManual
	}
	startedAt := time.Now().UnixMilli()
	run, err := s.store.CreateAntigravityInspectionRun(ctx, model.AntigravityInspectionRun{
		TriggerType:  triggerType,
		TriggerKey:   strings.TrimSpace(req.TriggerKey),
		Status:       model.AntigravityInspectionStatusRunning,
		StartedAtMS:  startedAt,
		Settings:     settings,
		SettingsJSON: model.MarshalAntigravityInspectionSettings(settings),
	})
	if err != nil {
		return RunDetail{}, err
	}
	persistCtx := context.WithoutCancel(ctx)
	logger := runLogger{service: s, runID: run.ID}
	logger.info(ctx, "Antigravity 巡检开始", map[string]any{"triggerType": triggerType, "triggerKey": strings.TrimSpace(req.TriggerKey), "targetType": settings.TargetType})

	files, err := s.fetchAuthFiles(ctx, setup)
	if err != nil {
		logger.error(persistCtx, "加载认证文件列表失败", map[string]any{"error": err.Error()})
		return s.failRun(persistCtx, run, err)
	}

	accounts := make([]account, 0, len(files))
	for _, file := range files {
		next := toAccount(file)
		if next.Provider == settings.TargetType {
			accounts = append(accounts, next)
		}
	}
	sampled := pickSample(accounts, settings.SampleSize)
	run.TotalFiles = len(files)
	run.ProbeSetCount = len(accounts)
	run.SampledCount = len(sampled)
	run.DisabledCount = countAccounts(sampled, true)
	run.EnabledCount = len(sampled) - run.DisabledCount
	_ = s.store.UpdateAntigravityInspectionRun(persistCtx, run)
	logger.info(ctx, "Antigravity 巡检集合已准备", map[string]any{"totalFiles": len(files), "probeSetCount": len(accounts), "sampledCount": len(sampled)})

	results := s.inspectAccounts(ctx, setup, settings, run.ID, sampled, logger)
	if err := ctx.Err(); err != nil {
		for _, result := range results {
			result.RunID = run.ID
			_, _ = s.store.InsertAntigravityInspectionResult(persistCtx, result)
		}
		run = summarizeRun(run, results)
		run.Status = model.AntigravityInspectionStatusFailed
		run.Error = err.Error()
		run.FinishedAtMS = time.Now().UnixMilli()
		if err := s.store.UpdateAntigravityInspectionRun(persistCtx, run); err != nil {
			return RunDetail{}, err
		}
		logger.warning(persistCtx, "Antigravity 巡检已取消", map[string]any{"error": run.Error})
		return s.GetRun(persistCtx, run.ID)
	}

	actionOutcomes := s.executeAutoActions(ctx, setup, settings, results, logger)
	results = applyActionOutcomes(results, actionOutcomes)
	for _, result := range results {
		result.RunID = run.ID
		_, _ = s.store.InsertAntigravityInspectionResult(persistCtx, result)
	}
	run = summarizeRun(run, results)
	if failed := countFailedOutcomes(actionOutcomes); failed > 0 {
		run.Error = fmt.Sprintf("%d 个自动处理动作执行失败，详见巡检日志", failed)
	}
	run.Status = model.AntigravityInspectionStatusCompleted
	run.FinishedAtMS = time.Now().UnixMilli()
	if err := s.store.UpdateAntigravityInspectionRun(persistCtx, run); err != nil {
		return RunDetail{}, err
	}
	logger.success(persistCtx, "Antigravity 巡检完成", map[string]any{"deleteCount": run.DeleteCount, "disableCount": run.DisableCount, "enableCount": run.EnableCount, "keepCount": run.KeepCount, "actionErrors": failedActionOutcomes(actionOutcomes)})
	return s.GetRun(persistCtx, run.ID)
}

func (s *Service) ListRuns(ctx context.Context, limit int) ([]model.AntigravityInspectionRun, error) {
	return s.store.ListAntigravityInspectionRuns(ctx, limit)
}

func (s *Service) GetRun(ctx context.Context, id int64) (RunDetail, error) {
	run, ok, err := s.store.GetAntigravityInspectionRun(ctx, id)
	if err != nil {
		return RunDetail{}, err
	}
	if !ok {
		return RunDetail{}, ErrRunNotFound
	}
	results, err := s.store.ListAntigravityInspectionResults(ctx, id)
	if err != nil {
		return RunDetail{}, err
	}
	logs, err := s.store.ListAntigravityInspectionLogs(ctx, id)
	if err != nil {
		return RunDetail{}, err
	}
	return RunDetail{Run: run, Results: results, Logs: logs}, nil
}

func (s *Service) ExecuteManualActions(ctx context.Context, runID int64, req ExecuteActionsRequest) (ExecuteActionsResult, error) {
	if err := s.acquireRun(); err != nil {
		return ExecuteActionsResult{}, err
	}
	defer s.releaseRun()
	if len(req.ResultIDs) == 0 {
		return ExecuteActionsResult{}, ErrActionIDsRequired
	}
	settings, setup, err := s.resolveRuntime(ctx)
	if err != nil {
		return ExecuteActionsResult{}, err
	}
	detail, err := s.GetRun(ctx, runID)
	if err != nil {
		return ExecuteActionsResult{}, err
	}
	if detail.Run.Status != model.AntigravityInspectionStatusCompleted {
		return ExecuteActionsResult{}, ErrRunNotCompleted
	}
	if detail.Run.Settings.TargetType != "" {
		settings = detail.Run.Settings
	}
	selected := map[int64]struct{}{}
	for _, id := range req.ResultIDs {
		if id > 0 {
			selected[id] = struct{}{}
		}
	}
	if len(selected) == 0 {
		return ExecuteActionsResult{}, ErrActionIDsRequired
	}
	items, preflightOutcomes := selectManualActionItems(detail.Results, selected)
	if len(items) == 0 && len(preflightOutcomes) == 0 {
		return ExecuteActionsResult{}, ErrNoActionableResults
	}

	persistCtx := context.WithoutCancel(ctx)
	logger := runLogger{service: s, runID: detail.Run.ID}
	logger.info(persistCtx, "手动处理 Antigravity 账号开始", map[string]any{"requestedCount": len(req.ResultIDs), "actionCount": len(items)})
	outcomes := make([]ActionOutcome, 0, len(preflightOutcomes)+len(items))
	outcomes = append(outcomes, preflightOutcomes...)
	outcomes = append(outcomes, s.executeActionItems(ctx, setup, settings, items, logger, "手动处理", func(item model.AntigravityInspectionResult) string { return item.Action })...)
	if len(outcomes) == 0 {
		return ExecuteActionsResult{}, ErrNoActionableResults
	}
	nextResults := applyActionOutcomes(detail.Results, outcomes)
	for _, result := range nextResults {
		result.RunID = detail.Run.ID
		_, _ = s.store.InsertAntigravityInspectionResult(persistCtx, result)
	}
	run := summarizeRun(detail.Run, nextResults)
	if failed := countFailedOutcomes(outcomes); failed > 0 {
		run.Error = fmt.Sprintf("%d 个手动处理动作执行失败，详见巡检日志", failed)
	} else {
		run.Error = ""
	}
	if err := s.store.UpdateAntigravityInspectionRun(persistCtx, run); err != nil {
		return ExecuteActionsResult{}, err
	}
	logger.success(persistCtx, "手动处理 Antigravity 账号完成", map[string]any{"successCount": len(outcomes) - countFailedOutcomes(outcomes), "failedCount": countFailedOutcomes(outcomes)})
	nextDetail, err := s.GetRun(persistCtx, detail.Run.ID)
	if err != nil {
		return ExecuteActionsResult{}, err
	}
	return ExecuteActionsResult{Outcomes: outcomes, Detail: nextDetail}, nil
}

func (s *Service) ResolveConfig(ctx context.Context) (model.ManagerAntigravityInspectionConfig, bool, error) {
	managerCfg, _, ok, err := s.managerConfigService.ResolveManagerConfigWithSource(ctx)
	if err != nil {
		return model.ManagerAntigravityInspectionConfig{}, false, err
	}
	if !ok || strings.TrimSpace(managerCfg.CPAConnection.CPABaseURL) == "" || strings.TrimSpace(managerCfg.CPAConnection.ManagementKey) == "" {
		return model.DefaultAntigravityInspectionConfig(), false, nil
	}
	return model.NormalizeAntigravityInspectionConfig(managerCfg.AntigravityInspection, model.DefaultAntigravityInspectionConfig()), true, nil
}

func (s *Service) acquireRun() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return ErrRunAlreadyActive
	}
	s.running = true
	return nil
}

func (s *Service) releaseRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
}

func (s *Service) resolveRuntime(ctx context.Context) (model.ManagerAntigravityInspectionConfig, store.Setup, error) {
	managerCfg, _, ok, err := s.managerConfigService.ResolveManagerConfigWithSource(ctx)
	if err != nil {
		return model.ManagerAntigravityInspectionConfig{}, store.Setup{}, err
	}
	if !ok || strings.TrimSpace(managerCfg.CPAConnection.CPABaseURL) == "" || strings.TrimSpace(managerCfg.CPAConnection.ManagementKey) == "" {
		return model.ManagerAntigravityInspectionConfig{}, store.Setup{}, ErrNotConfigured
	}
	settings := model.NormalizeAntigravityInspectionConfig(managerCfg.AntigravityInspection, model.DefaultAntigravityInspectionConfig())
	return settings, managerconfig.SetupFromManagerConfig(managerCfg), nil
}

func (s *Service) failRun(ctx context.Context, run model.AntigravityInspectionRun, cause error) (RunDetail, error) {
	run.Status = model.AntigravityInspectionStatusFailed
	run.Error = cause.Error()
	run.FinishedAtMS = time.Now().UnixMilli()
	_ = s.store.UpdateAntigravityInspectionRun(ctx, run)
	detail, err := s.GetRun(ctx, run.ID)
	if err != nil {
		return RunDetail{}, err
	}
	return detail, cause
}

func (s *Service) inspectAccounts(ctx context.Context, setup store.Setup, settings model.ManagerAntigravityInspectionConfig, runID int64, accounts []account, logger runLogger) []model.AntigravityInspectionResult {
	if len(accounts) == 0 {
		return nil
	}
	workers := settings.Workers
	if workers <= 0 {
		workers = 1
	}
	jobs := make(chan account)
	results := make(chan model.AntigravityInspectionResult, len(accounts))
	var wg sync.WaitGroup
	for i := 0; i < workers && i < len(accounts); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range jobs {
				result := s.inspectSingleAccount(ctx, setup, settings, item, logger)
				result.RunID = runID
				if _, err := s.store.InsertAntigravityInspectionResult(ctx, result); err != nil {
					logger.error(ctx, "写入 Antigravity 巡检账号结果失败", map[string]any{"fileName": item.FileName, "error": err.Error()})
				}
				results <- result
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, item := range accounts {
			select {
			case <-ctx.Done():
				return
			case jobs <- item:
			}
		}
	}()
	wg.Wait()
	close(results)
	out := make([]model.AntigravityInspectionResult, 0, len(accounts))
	for result := range results {
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FileName == out[j].FileName {
			return out[i].DisplayAccount < out[j].DisplayAccount
		}
		return out[i].FileName < out[j].FileName
	})
	return out
}

func pickSample(items []account, sampleSize int) []account {
	if sampleSize <= 0 || sampleSize >= len(items) {
		out := make([]account, len(items))
		copy(out, items)
		return out
	}
	out := make([]account, len(items))
	copy(out, items)
	rand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out[:sampleSize]
}

func countAccounts(items []account, disabled bool) int {
	count := 0
	for _, item := range items {
		if item.Disabled == disabled {
			count++
		}
	}
	return count
}
