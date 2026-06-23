package model

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	AntigravityInspectionScheduleModeInterval   = CodexInspectionScheduleModeInterval
	AntigravityInspectionScheduleModeTimePoints = CodexInspectionScheduleModeTimePoints

	AntigravityInspectionAutoActionNone    = CodexInspectionAutoActionNone
	AntigravityInspectionAutoActionEnable  = CodexInspectionAutoActionEnable
	AntigravityInspectionAutoActionDisable = CodexInspectionAutoActionDisable
	AntigravityInspectionAutoActionDelete  = CodexInspectionAutoActionDelete

	AntigravityInspectionRateLimitActionCooldown = "cooldown"
	AntigravityInspectionRateLimitActionNone     = "none"

	AntigravityInspectionStatusRunning   = CodexInspectionStatusRunning
	AntigravityInspectionStatusCompleted = CodexInspectionStatusCompleted
	AntigravityInspectionStatusFailed    = CodexInspectionStatusFailed

	AntigravityInspectionTriggerManual    = CodexInspectionTriggerManual
	AntigravityInspectionTriggerScheduled = CodexInspectionTriggerScheduled

	AntigravityInspectionActionStatusNone        = CodexInspectionActionStatusNone
	AntigravityInspectionActionStatusPending     = CodexInspectionActionStatusPending
	AntigravityInspectionActionStatusSuccess     = CodexInspectionActionStatusSuccess
	AntigravityInspectionActionStatusFailed      = CodexInspectionActionStatusFailed
	AntigravityInspectionActionStatusSkipped     = CodexInspectionActionStatusSkipped
	AntigravityInspectionActionStatusNeedsReview = CodexInspectionActionStatusNeedsReview
)

type ManagerAntigravityInspectionConfig struct {
	Enabled              *bool                                `json:"enabled,omitempty"`
	Schedule             ManagerCodexInspectionScheduleConfig `json:"schedule"`
	TargetType           string                               `json:"targetType,omitempty"`
	Workers              int                                  `json:"workers,omitempty"`
	DeleteWorkers        int                                  `json:"deleteWorkers,omitempty"`
	Timeout              int                                  `json:"timeout,omitempty"`
	Retries              int                                  `json:"retries,omitempty"`
	UserAgent            string                               `json:"userAgent,omitempty"`
	UsedPercentThreshold float64                              `json:"usedPercentThreshold,omitempty"`
	SampleSize           int                                  `json:"sampleSize,omitempty"`
	AutoActionMode       string                               `json:"autoActionMode,omitempty"`
	RateLimitAction      string                               `json:"rateLimitAction,omitempty"`
}

type AntigravityInspectionRun struct {
	ID            int64                               `json:"id"`
	TriggerType   string                              `json:"triggerType"`
	TriggerKey    string                              `json:"triggerKey,omitempty"`
	Status        string                              `json:"status"`
	StartedAtMS   int64                               `json:"startedAtMs"`
	FinishedAtMS  int64                               `json:"finishedAtMs,omitempty"`
	TotalFiles    int                                 `json:"totalFiles"`
	ProbeSetCount int                                 `json:"probeSetCount"`
	SampledCount  int                                 `json:"sampledCount"`
	DisabledCount int                                 `json:"disabledCount"`
	EnabledCount  int                                 `json:"enabledCount"`
	DeleteCount   int                                 `json:"deleteCount"`
	DisableCount  int                                 `json:"disableCount"`
	EnableCount   int                                 `json:"enableCount"`
	ReauthCount   int                                 `json:"reauthCount"`
	KeepCount     int                                 `json:"keepCount"`
	Error         string                              `json:"error,omitempty"`
	Settings      ManagerAntigravityInspectionConfig `json:"settings"`
	SettingsJSON  string                              `json:"-"`
	CreatedAtMS   int64                               `json:"createdAtMs"`
	UpdatedAtMS   int64                               `json:"updatedAtMs"`
}

type AntigravityInspectionResult struct {
	ID               int64                        `json:"id"`
	RunID            int64                        `json:"runId"`
	AccountKey       string                       `json:"accountKey"`
	FileName         string                       `json:"fileName"`
	DisplayAccount   string                       `json:"displayAccount"`
	AuthIndex        string                       `json:"authIndex,omitempty"`
	AccountID        string                       `json:"accountId,omitempty"`
	Provider         string                       `json:"provider"`
	Disabled         bool                         `json:"disabled"`
	Status           string                       `json:"status,omitempty"`
	State            string                       `json:"state,omitempty"`
	Action           string                       `json:"action"`
	ActionReason     string                       `json:"actionReason"`
	ActionStatus     string                       `json:"actionStatus,omitempty"`
	ExecutedAction   string                       `json:"executedAction,omitempty"`
	ActionError      string                       `json:"actionError,omitempty"`
	StatusCode       *int                         `json:"statusCode,omitempty"`
	UsedPercent      *float64                     `json:"usedPercent,omitempty"`
	IsQuota          bool                         `json:"isQuota"`
	Error            string                       `json:"error,omitempty"`
	PlanType         string                       `json:"planType,omitempty"`
	QuotaWindows     []CodexInspectionQuotaWindow `json:"quotaWindows,omitempty"`
	QuotaWindowsJSON string                       `json:"-"`
	RecoverAtMS      int64                        `json:"recoverAtMs,omitempty"`
	RecoverLabel     string                       `json:"recoverLabel,omitempty"`
	ErrorKind        string                       `json:"errorKind,omitempty"`
	ErrorDetail      string                       `json:"errorDetail,omitempty"`
	CreatedAtMS      int64                        `json:"createdAtMs"`
}

type AntigravityInspectionLog struct {
	ID          int64  `json:"id"`
	RunID       int64  `json:"runId"`
	Level       string `json:"level"`
	Message     string `json:"message"`
	DetailJSON  string `json:"-"`
	Detail      any    `json:"detail,omitempty"`
	CreatedAtMS int64  `json:"createdAtMs"`
}

func DefaultAntigravityInspectionConfig() ManagerAntigravityInspectionConfig {
	return ManagerAntigravityInspectionConfig{
		Enabled: boolPtr(false),
		Schedule: ManagerCodexInspectionScheduleConfig{
			Mode:            AntigravityInspectionScheduleModeInterval,
			IntervalMinutes: 60,
		},
		TargetType:           "antigravity",
		Workers:              4,
		DeleteWorkers:        4,
		Timeout:              15000,
		Retries:              0,
		UserAgent:            "antigravity/1.11.5 windows/amd64",
		UsedPercentThreshold: 100,
		SampleSize:           0,
		AutoActionMode:       AntigravityInspectionAutoActionDelete,
		RateLimitAction:      AntigravityInspectionRateLimitActionCooldown,
	}
}

func NormalizeAntigravityInspectionConfig(input ManagerAntigravityInspectionConfig, fallback ManagerAntigravityInspectionConfig) ManagerAntigravityInspectionConfig {
	base := fallback
	if base.TargetType == "" {
		base = DefaultAntigravityInspectionConfig()
	}

	next := base
	if input.Enabled != nil {
		next.Enabled = boolPtr(*input.Enabled)
	}
	next.Schedule = NormalizeCodexInspectionSchedule(input.Schedule, base.Schedule)
	next.TargetType = valueOrLower(input.TargetType, base.TargetType)
	next.Workers = positiveOr(input.Workers, base.Workers)
	next.DeleteWorkers = positiveOr(input.DeleteWorkers, positiveOr(input.Workers, base.DeleteWorkers))
	next.Timeout = positiveOr(input.Timeout, base.Timeout)
	if input.Retries >= 0 {
		next.Retries = input.Retries
	}
	next.UserAgent = valueOr(input.UserAgent, base.UserAgent)
	next.UsedPercentThreshold = normalizePercent(input.UsedPercentThreshold, base.UsedPercentThreshold)
	if input.SampleSize >= 0 {
		next.SampleSize = input.SampleSize
	}
	next.AutoActionMode = NormalizeAntigravityInspectionAutoActionMode(input.AutoActionMode, base.AutoActionMode)
	next.RateLimitAction = NormalizeAntigravityInspectionRateLimitAction(input.RateLimitAction, base.RateLimitAction)
	return next
}

func ValidateAntigravityInspectionConfig(input ManagerAntigravityInspectionConfig) error {
	return ValidateCodexInspectionSchedule(input.Schedule)
}

func NormalizeAntigravityInspectionAutoActionMode(value string, fallback string) string {
	return NormalizeCodexInspectionAutoActionMode(value, fallback)
}

func NormalizeAntigravityInspectionRateLimitAction(value string, fallback string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case AntigravityInspectionRateLimitActionNone:
		return AntigravityInspectionRateLimitActionNone
	case AntigravityInspectionRateLimitActionCooldown:
		return AntigravityInspectionRateLimitActionCooldown
	default:
		if fallback == AntigravityInspectionRateLimitActionNone {
			return fallback
		}
		return AntigravityInspectionRateLimitActionCooldown
	}
}

func NormalizeAntigravityInspectionActionStatus(value string, action string) string {
	return NormalizeCodexInspectionActionStatus(value, action)
}

func MarshalAntigravityInspectionSettings(settings ManagerAntigravityInspectionConfig) string {
	data, err := json.Marshal(settings)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func UnmarshalAntigravityInspectionSettings(raw string) ManagerAntigravityInspectionConfig {
	settings := DefaultAntigravityInspectionConfig()
	if strings.TrimSpace(raw) == "" {
		return settings
	}
	var parsed ManagerAntigravityInspectionConfig
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return settings
	}
	return NormalizeAntigravityInspectionConfig(parsed, settings)
}

func MarshalAntigravityInspectionQuotaWindows(windows []CodexInspectionQuotaWindow) string {
	return MarshalCodexInspectionQuotaWindows(windows)
}

func UnmarshalAntigravityInspectionQuotaWindows(raw string) []CodexInspectionQuotaWindow {
	return UnmarshalCodexInspectionQuotaWindows(raw)
}

func AntigravityInspectionTriggerKey(now time.Time, cfg ManagerAntigravityInspectionConfig) string {
	return CodexInspectionTriggerKey(now, ManagerCodexInspectionConfig{Schedule: cfg.Schedule})
}

func AntigravityInspectionScheduleDue(now time.Time, lastRun time.Time, cfg ManagerAntigravityInspectionConfig) bool {
	return CodexInspectionScheduleDue(now, lastRun, ManagerCodexInspectionConfig{
		Enabled:  cfg.Enabled,
		Schedule: cfg.Schedule,
	})
}
