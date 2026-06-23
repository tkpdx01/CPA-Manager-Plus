package antigravityinspection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/model"
)

type Repository interface {
	CreateRun(ctx context.Context, run model.AntigravityInspectionRun) (model.AntigravityInspectionRun, error)
	UpdateRun(ctx context.Context, run model.AntigravityInspectionRun) error
	InsertResult(ctx context.Context, result model.AntigravityInspectionResult) (model.AntigravityInspectionResult, error)
	InsertLog(ctx context.Context, entry model.AntigravityInspectionLog) (model.AntigravityInspectionLog, error)
	ListRuns(ctx context.Context, limit int) ([]model.AntigravityInspectionRun, error)
	GetRun(ctx context.Context, id int64) (model.AntigravityInspectionRun, bool, error)
	GetLatestRunByTrigger(ctx context.Context, triggerType, triggerKey string) (model.AntigravityInspectionRun, bool, error)
	ListResults(ctx context.Context, runID int64) ([]model.AntigravityInspectionResult, error)
	ListLogs(ctx context.Context, runID int64) ([]model.AntigravityInspectionLog, error)
}

type repository struct {
	db *sql.DB
}

func New(db *sql.DB) Repository {
	return &repository{db: db}
}

func (r *repository) CreateRun(ctx context.Context, run model.AntigravityInspectionRun) (model.AntigravityInspectionRun, error) {
	now := time.Now().UnixMilli()
	if run.StartedAtMS <= 0 {
		run.StartedAtMS = now
	}
	if run.CreatedAtMS <= 0 {
		run.CreatedAtMS = now
	}
	run.UpdatedAtMS = now
	if run.Status == "" {
		run.Status = model.AntigravityInspectionStatusRunning
	}
	if run.SettingsJSON == "" {
		run.SettingsJSON = model.MarshalAntigravityInspectionSettings(run.Settings)
	}
	res, err := r.db.ExecContext(
		ctx,
		`insert into antigravity_inspection_runs (
			trigger_type, trigger_key, status, started_at_ms, finished_at_ms,
			total_files, probe_set_count, sampled_count, disabled_count, enabled_count,
			delete_count, disable_count, enable_count, reauth_count, keep_count, error,
			settings_json, created_at_ms, updated_at_ms
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.TriggerType,
		nullString(run.TriggerKey),
		run.Status,
		run.StartedAtMS,
		nullPositiveInt64(run.FinishedAtMS),
		run.TotalFiles,
		run.ProbeSetCount,
		run.SampledCount,
		run.DisabledCount,
		run.EnabledCount,
		run.DeleteCount,
		run.DisableCount,
		run.EnableCount,
		run.ReauthCount,
		run.KeepCount,
		nullString(run.Error),
		run.SettingsJSON,
		run.CreatedAtMS,
		run.UpdatedAtMS,
	)
	if err != nil {
		return model.AntigravityInspectionRun{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return model.AntigravityInspectionRun{}, err
	}
	run.ID = id
	return run, nil
}

func (r *repository) UpdateRun(ctx context.Context, run model.AntigravityInspectionRun) error {
	if run.ID <= 0 {
		return errors.New("antigravity inspection run id is required")
	}
	run.UpdatedAtMS = time.Now().UnixMilli()
	if run.SettingsJSON == "" {
		run.SettingsJSON = model.MarshalAntigravityInspectionSettings(run.Settings)
	}
	_, err := r.db.ExecContext(
		ctx,
		`update antigravity_inspection_runs set
			status = ?,
			finished_at_ms = ?,
			total_files = ?,
			probe_set_count = ?,
			sampled_count = ?,
			disabled_count = ?,
			enabled_count = ?,
			delete_count = ?,
			disable_count = ?,
			enable_count = ?,
			reauth_count = ?,
			keep_count = ?,
			error = ?,
			settings_json = ?,
			updated_at_ms = ?
		where id = ?`,
		run.Status,
		nullPositiveInt64(run.FinishedAtMS),
		run.TotalFiles,
		run.ProbeSetCount,
		run.SampledCount,
		run.DisabledCount,
		run.EnabledCount,
		run.DeleteCount,
		run.DisableCount,
		run.EnableCount,
		run.ReauthCount,
		run.KeepCount,
		nullString(run.Error),
		run.SettingsJSON,
		run.UpdatedAtMS,
		run.ID,
	)
	return err
}

func (r *repository) InsertResult(ctx context.Context, result model.AntigravityInspectionResult) (model.AntigravityInspectionResult, error) {
	if result.CreatedAtMS <= 0 {
		result.CreatedAtMS = time.Now().UnixMilli()
	}
	if result.QuotaWindowsJSON == "" && len(result.QuotaWindows) > 0 {
		result.QuotaWindowsJSON = model.MarshalAntigravityInspectionQuotaWindows(result.QuotaWindows)
	}
	result.ActionStatus = model.NormalizeAntigravityInspectionActionStatus(result.ActionStatus, result.Action)
	disabled := 0
	if result.Disabled {
		disabled = 1
	}
	isQuota := 0
	if result.IsQuota {
		isQuota = 1
	}
	res, err := r.db.ExecContext(
		ctx,
		`insert into antigravity_inspection_results (
			run_id, account_key, file_name, display_account, auth_index, account_id,
			provider, disabled, status, state, action, action_reason, status_code,
			used_percent, is_quota, error, action_status, executed_action, action_error,
			plan_type, quota_windows_json, recover_at_ms, recover_label, error_kind, error_detail, created_at_ms
		) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(run_id, account_key) do update set
			file_name = excluded.file_name,
			display_account = excluded.display_account,
			auth_index = excluded.auth_index,
			account_id = excluded.account_id,
			provider = excluded.provider,
			disabled = excluded.disabled,
			status = excluded.status,
			state = excluded.state,
			action = excluded.action,
			action_reason = excluded.action_reason,
			status_code = excluded.status_code,
			used_percent = excluded.used_percent,
			is_quota = excluded.is_quota,
			error = excluded.error,
			action_status = excluded.action_status,
			executed_action = excluded.executed_action,
			action_error = excluded.action_error,
			plan_type = excluded.plan_type,
			quota_windows_json = excluded.quota_windows_json,
			recover_at_ms = excluded.recover_at_ms,
			recover_label = excluded.recover_label,
			error_kind = excluded.error_kind,
			error_detail = excluded.error_detail,
			created_at_ms = excluded.created_at_ms`,
		result.RunID,
		result.AccountKey,
		result.FileName,
		result.DisplayAccount,
		nullString(result.AuthIndex),
		nullString(result.AccountID),
		nullString(result.Provider),
		disabled,
		nullString(result.Status),
		nullString(result.State),
		result.Action,
		nullString(result.ActionReason),
		nullInt(result.StatusCode),
		nullFloat(result.UsedPercent),
		isQuota,
		nullString(result.Error),
		nullString(result.ActionStatus),
		nullString(result.ExecutedAction),
		nullString(result.ActionError),
		nullString(result.PlanType),
		nullString(result.QuotaWindowsJSON),
		nullPositiveInt64(result.RecoverAtMS),
		nullString(result.RecoverLabel),
		nullString(result.ErrorKind),
		nullString(result.ErrorDetail),
		result.CreatedAtMS,
	)
	if err != nil {
		return model.AntigravityInspectionResult{}, err
	}
	id, _ := res.LastInsertId()
	result.ID = id
	return result, nil
}

func (r *repository) InsertLog(ctx context.Context, entry model.AntigravityInspectionLog) (model.AntigravityInspectionLog, error) {
	if entry.CreatedAtMS <= 0 {
		entry.CreatedAtMS = time.Now().UnixMilli()
	}
	if entry.DetailJSON == "" && entry.Detail != nil {
		if data, err := json.Marshal(entry.Detail); err == nil {
			entry.DetailJSON = string(data)
		}
	}
	res, err := r.db.ExecContext(
		ctx,
		`insert into antigravity_inspection_logs(run_id, level, message, detail_json, created_at_ms)
		 values(?, ?, ?, ?, ?)`,
		entry.RunID,
		entry.Level,
		entry.Message,
		nullString(entry.DetailJSON),
		entry.CreatedAtMS,
	)
	if err != nil {
		return model.AntigravityInspectionLog{}, err
	}
	id, _ := res.LastInsertId()
	entry.ID = id
	return entry, nil
}

func (r *repository) ListRuns(ctx context.Context, limit int) ([]model.AntigravityInspectionRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := r.db.QueryContext(
		ctx,
		`select
			id, trigger_type, trigger_key, status, started_at_ms, finished_at_ms,
			total_files, probe_set_count, sampled_count, disabled_count, enabled_count,
			delete_count, disable_count, enable_count, reauth_count, keep_count, error,
			settings_json, created_at_ms, updated_at_ms
		from antigravity_inspection_runs
		order by started_at_ms desc, id desc
		limit ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := make([]model.AntigravityInspectionRun, 0)
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (r *repository) GetRun(ctx context.Context, id int64) (model.AntigravityInspectionRun, bool, error) {
	row := r.db.QueryRowContext(
		ctx,
		`select
			id, trigger_type, trigger_key, status, started_at_ms, finished_at_ms,
			total_files, probe_set_count, sampled_count, disabled_count, enabled_count,
			delete_count, disable_count, enable_count, reauth_count, keep_count, error,
			settings_json, created_at_ms, updated_at_ms
		from antigravity_inspection_runs
		where id = ?`,
		id,
	)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AntigravityInspectionRun{}, false, nil
	}
	if err != nil {
		return model.AntigravityInspectionRun{}, false, err
	}
	return run, true, nil
}

func (r *repository) GetLatestRunByTrigger(ctx context.Context, triggerType, triggerKey string) (model.AntigravityInspectionRun, bool, error) {
	row := r.db.QueryRowContext(
		ctx,
		`select
			id, trigger_type, trigger_key, status, started_at_ms, finished_at_ms,
			total_files, probe_set_count, sampled_count, disabled_count, enabled_count,
			delete_count, disable_count, enable_count, reauth_count, keep_count, error,
			settings_json, created_at_ms, updated_at_ms
		from antigravity_inspection_runs
		where trigger_type = ? and trigger_key = ?
		order by started_at_ms desc, id desc
		limit 1`,
		triggerType,
		triggerKey,
	)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.AntigravityInspectionRun{}, false, nil
	}
	if err != nil {
		return model.AntigravityInspectionRun{}, false, err
	}
	return run, true, nil
}

func (r *repository) ListResults(ctx context.Context, runID int64) ([]model.AntigravityInspectionResult, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`select
			id, run_id, account_key, file_name, display_account, auth_index, account_id,
			provider, disabled, status, state, action, action_reason, status_code,
			used_percent, is_quota, error, action_status, executed_action, action_error,
			plan_type, quota_windows_json, recover_at_ms, recover_label, error_kind, error_detail, created_at_ms
		from antigravity_inspection_results
		where run_id = ?
		order by file_name asc, display_account asc, id asc`,
		runID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]model.AntigravityInspectionResult, 0)
	for rows.Next() {
		result, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (r *repository) ListLogs(ctx context.Context, runID int64) ([]model.AntigravityInspectionLog, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`select id, run_id, level, message, detail_json, created_at_ms
		from antigravity_inspection_logs
		where run_id = ?
		order by created_at_ms asc, id asc`,
		runID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	logs := make([]model.AntigravityInspectionLog, 0)
	for rows.Next() {
		entry, err := scanLog(rows)
		if err != nil {
			return nil, err
		}
		logs = append(logs, entry)
	}
	return logs, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRun(row scanner) (model.AntigravityInspectionRun, error) {
	var run model.AntigravityInspectionRun
	var triggerKey, errorText sql.NullString
	var finishedAt sql.NullInt64
	if err := row.Scan(
		&run.ID,
		&run.TriggerType,
		&triggerKey,
		&run.Status,
		&run.StartedAtMS,
		&finishedAt,
		&run.TotalFiles,
		&run.ProbeSetCount,
		&run.SampledCount,
		&run.DisabledCount,
		&run.EnabledCount,
		&run.DeleteCount,
		&run.DisableCount,
		&run.EnableCount,
		&run.ReauthCount,
		&run.KeepCount,
		&errorText,
		&run.SettingsJSON,
		&run.CreatedAtMS,
		&run.UpdatedAtMS,
	); err != nil {
		return model.AntigravityInspectionRun{}, err
	}
	run.TriggerKey = triggerKey.String
	run.Error = errorText.String
	if finishedAt.Valid {
		run.FinishedAtMS = finishedAt.Int64
	}
	run.Settings = model.UnmarshalAntigravityInspectionSettings(run.SettingsJSON)
	return run, nil
}

func scanResult(row scanner) (model.AntigravityInspectionResult, error) {
	var result model.AntigravityInspectionResult
	var authIndex, accountID, provider, status, state, actionReason, errorText sql.NullString
	var actionStatus, executedAction, actionError sql.NullString
	var planType, quotaWindowsJSON, recoverLabel, errorKind, errorDetail sql.NullString
	var statusCode, recoverAtMS sql.NullInt64
	var usedPercent sql.NullFloat64
	var disabled, isQuota int
	if err := row.Scan(
		&result.ID,
		&result.RunID,
		&result.AccountKey,
		&result.FileName,
		&result.DisplayAccount,
		&authIndex,
		&accountID,
		&provider,
		&disabled,
		&status,
		&state,
		&result.Action,
		&actionReason,
		&statusCode,
		&usedPercent,
		&isQuota,
		&errorText,
		&actionStatus,
		&executedAction,
		&actionError,
		&planType,
		&quotaWindowsJSON,
		&recoverAtMS,
		&recoverLabel,
		&errorKind,
		&errorDetail,
		&result.CreatedAtMS,
	); err != nil {
		return model.AntigravityInspectionResult{}, err
	}
	result.AuthIndex = authIndex.String
	result.AccountID = accountID.String
	result.Provider = provider.String
	result.Disabled = disabled != 0
	result.Status = status.String
	result.State = state.String
	result.ActionReason = actionReason.String
	result.IsQuota = isQuota != 0
	result.Error = errorText.String
	result.ActionStatus = model.NormalizeAntigravityInspectionActionStatus(actionStatus.String, result.Action)
	result.ExecutedAction = executedAction.String
	result.ActionError = actionError.String
	result.PlanType = planType.String
	result.QuotaWindowsJSON = quotaWindowsJSON.String
	result.QuotaWindows = model.UnmarshalAntigravityInspectionQuotaWindows(result.QuotaWindowsJSON)
	if recoverAtMS.Valid {
		result.RecoverAtMS = recoverAtMS.Int64
	}
	result.RecoverLabel = recoverLabel.String
	result.ErrorKind = errorKind.String
	result.ErrorDetail = errorDetail.String
	if statusCode.Valid {
		value := int(statusCode.Int64)
		result.StatusCode = &value
	}
	if usedPercent.Valid {
		value := usedPercent.Float64
		result.UsedPercent = &value
	}
	return result, nil
}

func scanLog(row scanner) (model.AntigravityInspectionLog, error) {
	var entry model.AntigravityInspectionLog
	var detail sql.NullString
	if err := row.Scan(
		&entry.ID,
		&entry.RunID,
		&entry.Level,
		&entry.Message,
		&detail,
		&entry.CreatedAtMS,
	); err != nil {
		return model.AntigravityInspectionLog{}, err
	}
	entry.DetailJSON = detail.String
	if detail.Valid && detail.String != "" {
		var parsed any
		if err := json.Unmarshal([]byte(detail.String), &parsed); err == nil {
			entry.Detail = parsed
		}
	}
	return entry, nil
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullPositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}
