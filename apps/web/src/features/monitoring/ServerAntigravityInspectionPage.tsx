import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import {
  IconChartLine,
  IconCheck,
  IconInbox,
  IconRefreshCw,
  IconShield,
  IconTrash2,
} from '@/components/ui/icons';
import { Input } from '@/components/ui/Input';
import { Select, type SelectOption } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { CodexInspectionConfigOverview } from '@/features/monitoring/components/CodexInspectionConfigOverview';
import { CodexInspectionModeTabs } from '@/features/monitoring/components/CodexInspectionModeTabs';
import { Panel } from '@/features/monitoring/components/CodexInspectionPanels';
import { CodexInspectionResultsPanel } from '@/features/monitoring/components/CodexInspectionResultsPanel';
import { InspectionConfigDrawer } from '@/features/monitoring/components/InspectionConfigDrawer';
import { InspectionConfigFields } from '@/features/monitoring/components/InspectionConfigFields';
import {
  type CodexInspectionAction,
  type CodexInspectionResultItem,
  type CodexInspectionRunResult,
} from '@/features/monitoring/codexInspection';
import {
  CODEX_INSPECTION_RESULT_PAGE_SIZE_OPTIONS,
  buildCodexInspectionPaginationState,
  buildConfigOverviewItems,
  type CodexInspectionSummaryAccent,
  countHandlingStates,
  filterInspectionResults,
  formatActionLabel,
  formatTimestamp,
  getActionFilterCounts,
  type ActionFilter,
  type HandlingFilter,
  type StatusTone,
  validateInspectionConfigDraft,
  validateInspectionConfigFields,
} from '@/features/monitoring/model/codexInspectionPresentation';
import { usePanelFeatureAvailability } from '@/hooks/usePanelFeatureAvailability';
import {
  getUsageServiceErrorCode,
  usageServiceApi,
  type AntigravityInspectionLog,
  type AntigravityInspectionResult,
  type AntigravityInspectionRun,
  type AntigravityInspectionRunDetail,
  type ManagerAntigravityInspectionConfig,
  type ManagerAntigravityInspectionScheduleMode,
  type ManagerConfig,
} from '@/services/api/usageService';
import { useAuthStore, useNotificationStore } from '@/stores';
import styles from './CodexInspectionPage.module.scss';

type ServerAntigravityInspectionDraft = {
  enabled: boolean;
  scheduleMode: ManagerAntigravityInspectionScheduleMode;
  intervalMinutes: string;
  timePoints: string;
  timeZone: string;
  targetType: string;
  workers: string;
  deleteWorkers: string;
  timeout: string;
  retries: string;
  userAgent: string;
  usedPercentThreshold: string;
  sampleSize: string;
  autoActionMode: string;
  rateLimitAction: string;
};

type NormalizedServerAntigravityInspectionConfig = {
  enabled: boolean;
  schedule: {
    mode: ManagerAntigravityInspectionScheduleMode;
    intervalMinutes: number;
    timePoints: string[];
    timeZone: string;
  };
  targetType: string;
  workers: number;
  deleteWorkers: number;
  timeout: number;
  retries: number;
  userAgent: string;
  usedPercentThreshold: number;
  sampleSize: number;
  autoActionMode: string;
  rateLimitAction: string;
};

const DEFAULT_SERVER_ANTIGRAVITY_CONFIG: NormalizedServerAntigravityInspectionConfig = {
  enabled: false,
  schedule: { mode: 'interval', intervalMinutes: 60, timePoints: [], timeZone: '' },
  targetType: 'antigravity',
  workers: 4,
  deleteWorkers: 4,
  timeout: 15000,
  retries: 0,
  userAgent: 'antigravity/1.11.5 windows/amd64',
  usedPercentThreshold: 100,
  sampleSize: 0,
  autoActionMode: 'delete',
  rateLimitAction: 'cooldown',
};

const RUNS_LIMIT = 30;

const COMMON_TIME_ZONES: ReadonlyArray<string> = [
  'UTC', 'Asia/Shanghai', 'Asia/Tokyo', 'Asia/Singapore', 'Asia/Hong_Kong',
  'Asia/Kolkata', 'Europe/London', 'Europe/Berlin', 'Europe/Moscow',
  'America/New_York', 'America/Los_Angeles',
];

const detectBrowserTimeZone = (): string => {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || ''; }
  catch { return ''; }
};

const isScheduleMode = (value: unknown): value is ManagerAntigravityInspectionScheduleMode =>
  value === 'interval' || value === 'time_points';

const resolveServerAntigravityConfig = (
  config?: ManagerAntigravityInspectionConfig | null
): NormalizedServerAntigravityInspectionConfig => {
  const schedule = config?.schedule ?? {};
  const scheduleMode = isScheduleMode(schedule.mode)
    ? schedule.mode
    : schedule.timePoints && schedule.timePoints.length > 0 ? 'time_points' : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.schedule.mode;
  return {
    ...DEFAULT_SERVER_ANTIGRAVITY_CONFIG,
    ...config,
    enabled: config?.enabled ?? DEFAULT_SERVER_ANTIGRAVITY_CONFIG.enabled,
    schedule: {
      mode: scheduleMode,
      intervalMinutes: schedule.intervalMinutes && schedule.intervalMinutes > 0 ? schedule.intervalMinutes : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.schedule.intervalMinutes,
      timePoints: schedule.timePoints ?? DEFAULT_SERVER_ANTIGRAVITY_CONFIG.schedule.timePoints,
      timeZone: typeof schedule.timeZone === 'string' ? schedule.timeZone : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.schedule.timeZone,
    },
    targetType: config?.targetType || DEFAULT_SERVER_ANTIGRAVITY_CONFIG.targetType,
    workers: config?.workers && config.workers > 0 ? config.workers : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.workers,
    deleteWorkers: config?.deleteWorkers && config.deleteWorkers > 0 ? config.deleteWorkers : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.deleteWorkers,
    timeout: config?.timeout && config.timeout > 0 ? config.timeout : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.timeout,
    retries: config?.retries !== undefined && config.retries >= 0 ? config.retries : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.retries,
    userAgent: config?.userAgent || DEFAULT_SERVER_ANTIGRAVITY_CONFIG.userAgent,
    usedPercentThreshold: config?.usedPercentThreshold !== undefined ? config.usedPercentThreshold : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.usedPercentThreshold,
    sampleSize: config?.sampleSize !== undefined && config.sampleSize >= 0 ? config.sampleSize : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.sampleSize,
    autoActionMode: config?.autoActionMode || DEFAULT_SERVER_ANTIGRAVITY_CONFIG.autoActionMode,
    rateLimitAction: config?.rateLimitAction || DEFAULT_SERVER_ANTIGRAVITY_CONFIG.rateLimitAction,
  };
};

const toDraft = (config?: ManagerAntigravityInspectionConfig | null): ServerAntigravityInspectionDraft => {
  const resolved = resolveServerAntigravityConfig(config);
  return {
    enabled: resolved.enabled,
    scheduleMode: resolved.schedule.mode as ManagerAntigravityInspectionScheduleMode,
    intervalMinutes: String(resolved.schedule.intervalMinutes),
    timePoints: resolved.schedule.timePoints.join(', '),
    timeZone: resolved.schedule.timeZone,
    targetType: resolved.targetType,
    workers: String(resolved.workers),
    deleteWorkers: String(resolved.deleteWorkers),
    timeout: String(resolved.timeout),
    retries: String(resolved.retries),
    userAgent: resolved.userAgent,
    usedPercentThreshold: String(resolved.usedPercentThreshold),
    sampleSize: String(resolved.sampleSize),
    autoActionMode: resolved.autoActionMode,
    rateLimitAction: resolved.rateLimitAction,
  };
};

type AntigravityInspectionDraftField = keyof ServerAntigravityInspectionDraft;

const normalizeTimePoint = (value: string): string | null => {
  const match = value.trim().match(/^(\d{1,2}):(\d{1,2})$/);
  if (!match) return null;
  const hour = Number(match[1]), minute = Number(match[2]);
  if (hour < 0 || hour > 23 || minute < 0 || minute > 59) return null;
  return `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`;
};

const splitTimePointTokens = (raw: string): string[] =>
  raw.split(/[\s,;，；]+/).map(v => v.trim()).filter(Boolean);

const parseTimePoints = (raw: string): string[] =>
  Array.from(new Set(splitTimePointTokens(raw).map(normalizeTimePoint).filter((v): v is string => Boolean(v)))).sort();

const formatSchedule = (config: NormalizedServerAntigravityInspectionConfig, t: ReturnType<typeof useTranslation>['t']) => {
  if (config.schedule.mode === 'time_points') {
    const base = t('monitoring.server_antigravity_inspection_schedule_time_points_value', { points: config.schedule.timePoints.join(', ') });
    const tz = config.schedule.timeZone?.trim();
    return tz ? `${base} (${tz})` : base;
  }
  return t('monitoring.server_antigravity_inspection_schedule_interval_value', { minutes: config.schedule.intervalMinutes });
};

function getRunTone(run?: AntigravityInspectionRun | null): StatusTone {
  switch (run?.status) { case 'completed': return 'good'; case 'failed': return 'bad'; case 'running': return 'info'; default: return 'idle'; }
}

function getRunStatusLabel(run: AntigravityInspectionRun | null | undefined, t: ReturnType<typeof useTranslation>['t']) {
  switch (run?.status) { case 'completed': return t('monitoring.codex_inspection_status_success'); case 'failed': return t('monitoring.codex_inspection_status_error'); case 'running': return t('monitoring.codex_inspection_status_running'); default: return t('monitoring.codex_inspection_status_idle'); }
}

function formatTrigger(run: AntigravityInspectionRun | null | undefined, t: ReturnType<typeof useTranslation>['t']) {
  if (!run) return t('common.not_set');
  return run.triggerType === 'scheduled' ? t('monitoring.server_codex_inspection_trigger_scheduled') : t('monitoring.server_codex_inspection_trigger_manual');
}

function formatResultStateHeader(run: AntigravityInspectionRun | null | undefined, t: ReturnType<typeof useTranslation>['t']) {
  if (run?.triggerType === 'scheduled') return t('monitoring.server_codex_inspection_result_state_scheduled');
  if (run?.triggerType === 'manual') return t('monitoring.server_codex_inspection_result_state_manual');
  return t('monitoring.server_codex_inspection_result_state_snapshot');
}

function formatResultsDescription(run: AntigravityInspectionRun | null | undefined, locale: string, t: ReturnType<typeof useTranslation>['t']) {
  const time = run?.finishedAtMs ? formatTimestamp(run.finishedAtMs, locale) : t('common.not_set');
  if (run?.triggerType === 'manual') return t('monitoring.server_codex_inspection_results_desc_manual', { time });
  if (run?.triggerType === 'scheduled') return t('monitoring.server_codex_inspection_results_desc_scheduled', { time });
  return t('monitoring.server_codex_inspection_results_desc');
}

const statusToneClass: Record<StatusTone, string> = {
  idle: styles['tone-idle'], info: styles['tone-info'], good: styles['tone-good'], warn: styles['tone-warn'], bad: styles['tone-bad'],
};

const summaryAccentClassMap: Record<CodexInspectionSummaryAccent, string> = {
  blue: styles.summaryAccentBlue, cyan: styles.summaryAccentCyan, red: styles.summaryAccentRed, amber: styles.summaryAccentAmber, green: styles.summaryAccentGreen, violet: styles.summaryAccentViolet,
};

const logLevelClass: Record<string, string> = {
  info: styles.logInfo, success: styles.logSuccess, warning: styles.logWarning, error: styles.logError,
};

function resolveActionLabel(action: string, t: ReturnType<typeof useTranslation>['t']) {
  if (['delete', 'disable', 'enable', 'reauth', 'keep'].includes(action)) return formatActionLabel(action, t);
  return action || t('common.not_set');
}

function normalizeServerResultAction(action: string): CodexInspectionAction {
  if (['delete', 'disable', 'enable', 'reauth', 'keep'].includes(action)) return action as CodexInspectionAction;
  return 'keep';
}

function toServerResultItem(item: AntigravityInspectionResult, t: ReturnType<typeof useTranslation>['t']): CodexInspectionResultItem {
  return {
    key: `server-antigravity-${item.id || item.accountKey}`,
    fileName: item.fileName,
    displayAccount: item.displayAccount,
    authIndex: item.authIndex ?? null,
    accountId: item.accountId ?? null,
    provider: item.provider,
    disabled: item.disabled,
    status: item.status ?? '',
    state: item.state ?? '',
    raw: item as unknown as CodexInspectionResultItem['raw'],
    action: normalizeServerResultAction(item.action),
    actionReason: item.actionReason,
    statusCode: item.statusCode ?? null,
    usedPercent: item.usedPercent ?? null,
    isQuota: item.isQuota,
    error: item.error ?? '',
    planType: item.planType ?? null,
    quotaWindows: item.quotaWindows?.map(w => ({ id: w.id, labelKey: w.labelKey, labelParams: w.labelParams, usedPercent: w.usedPercent ?? null, resetLabel: w.resetLabel ?? '', limitWindowSeconds: w.limitWindowSeconds ?? null })),
    errorKind: item.errorKind,
    errorDetail: item.actionError || item.errorDetail || '',
  };
}

function getServerActionIcon(action: string) {
  if (action === 'delete') return IconTrash2;
  if (action === 'disable') return IconShield;
  return IconRefreshCw;
}

function getUsageServiceDisplayError(error: unknown, t: ReturnType<typeof useTranslation>['t']) {
  const code = getUsageServiceErrorCode(error);
  if (code) return t(`usage_service_errors.${code}`, { defaultValue: t('usage_service_errors.request_failed') });
  if (error instanceof Error && error.message) return error.message;
  return t('usage_service_errors.request_failed');
}

function isActionableServerResult(item: AntigravityInspectionResult): boolean {
  return item.action === 'delete' || item.action === 'disable' || item.action === 'enable';
}

function getCanonicalActionIds(results: AntigravityInspectionResult[]): Set<number> {
  const seenFiles = new Map<string, number>();
  const canonical = new Set<number>();
  results.forEach(item => {
    if (!isActionableServerResult(item)) return;
    const fn = item.fileName.trim();
    if (!fn) return;
    const existing = seenFiles.get(fn);
    if (existing === undefined) { seenFiles.set(fn, item.id); canonical.add(item.id); }
  });
  return canonical;
}

interface ValidationResult { ok: boolean; values: Record<string, number | string>; errors: Record<string, string> }

function validateDraft(draft: ServerAntigravityInspectionDraft, t: ReturnType<typeof useTranslation>['t']): ValidationResult {
  const errors: Record<string, string> = {};
  const intField = (key: AntigravityInspectionDraftField, min: number, label: string) => {
    const v = Number(draft[key]); if (!Number.isInteger(v) || v < min) errors[key] = `${label} >= ${min}`;
    return v;
  };
  const workers = intField('workers', 1, t('monitoring.codex_inspection_field_workers'));
  intField('deleteWorkers', 1, t('monitoring.codex_inspection_field_delete_workers'));
  intField('timeout', 1, t('monitoring.codex_inspection_field_timeout'));
  intField('retries', 0, t('monitoring.codex_inspection_field_retries'));
  intField('sampleSize', 0, t('monitoring.codex_inspection_field_sample_size'));
  const threshold = Number(draft.usedPercentThreshold);
  if (!Number.isFinite(threshold) || threshold < 0 || threshold > 100) errors.usedPercentThreshold = '0-100';
  const targetType = draft.targetType.trim() || 'antigravity';
  const autoActionMode = ['none', 'enable', 'disable', 'delete'].includes(draft.autoActionMode) ? draft.autoActionMode : 'delete';
  const rateLimitAction = ['cooldown', 'none'].includes(draft.rateLimitAction) ? draft.rateLimitAction : 'cooldown';
  return { ok: Object.keys(errors).length === 0, values: { workers, deleteWorkers: draft.deleteWorkers, timeout: draft.timeout, retries: draft.retries, usedPercentThreshold: threshold, sampleSize: draft.sampleSize, targetType, autoActionMode, rateLimitAction }, errors };
}

export function ServerAntigravityInspectionPage() {
  const { t, i18n } = useTranslation();
  const managementKey = useAuthStore(s => s.managementKey);
  const featureAvailability = usePanelFeatureAvailability();
  const showNotification = useNotificationStore(s => s.showNotification);
  const showConfirmation = useNotificationStore(s => s.showConfirmation);

  const [serviceBase, setServiceBase] = useState('');
  const [managerConfig, setManagerConfig] = useState<ManagerConfig | null>(null);
  const [draft, setDraft] = useState<ServerAntigravityInspectionDraft>(() => toDraft(null));
  const [runs, setRuns] = useState<AntigravityInspectionRun[]>([]);
  const [detail, setDetail] = useState<AntigravityInspectionRunDetail | null>(null);
  const [selectedRunId, setSelectedRunId] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState('');
  const [logsCollapsed, setLogsCollapsed] = useState(false);
  const [actionFilter, setActionFilter] = useState<ActionFilter>('all');
  const [handlingFilter, setHandlingFilter] = useState<HandlingFilter>('all');
  const [resultPage, setResultPage] = useState(1);
  const [resultPageSize, setResultPageSize] = useState<number>(CODEX_INSPECTION_RESULT_PAGE_SIZE_OPTIONS[0]);
  const [logLevelFilter, setLogLevelFilter] = useState<'all' | 'info' | 'success' | 'warning' | 'error'>('all');
  const [executingResultIds, setExecutingResultIds] = useState<Set<number>>(() => new Set());
  const [configDrawerOpen, setConfigDrawerOpen] = useState(false);

  const loadRunDetail = useCallback(async (base: string, id: number) => {
    const d = await usageServiceApi.getAntigravityInspectionRun(base, managementKey, id);
    setDetail(d); setSelectedRunId(d.run.id); return d;
  }, [managementKey]);

  const loadPageData = useCallback(async () => {
    setLoading(true); setError('');
    try {
      const resolvedBase = featureAvailability.managerServiceBase;
      if (!resolvedBase || !featureAvailability.serverCodexInspectionAvailable) throw new Error(t('monitoring.server_codex_inspection_service_unavailable'));
      const resp = await usageServiceApi.getManagerConfig(resolvedBase, managementKey);
      setServiceBase(resolvedBase); setManagerConfig(resp.config);
      setDraft(toDraft(resp.config.antigravityInspection));
      const runsResp = await usageServiceApi.listAntigravityInspectionRuns(resolvedBase, managementKey, RUNS_LIMIT);
      setRuns(runsResp.items);
      const nextId = runsResp.items[0]?.id;
      if (nextId) await loadRunDetail(resolvedBase, nextId);
      else { setDetail(null); setSelectedRunId(null); }
    } catch (err: unknown) { setError(getUsageServiceDisplayError(err, t)); setRuns([]); setDetail(null); setSelectedRunId(null); }
    finally { setLoading(false); }
  }, [featureAvailability.managerServiceBase, featureAvailability.serverCodexInspectionAvailable, loadRunDetail, managementKey, t]);

  useEffect(() => {
    if (featureAvailability.checking) return;
    if (!managementKey) { setLoading(false); setError(t('monitoring.server_codex_inspection_connection_required')); return; }
    if (!featureAvailability.serverCodexInspectionAvailable) { setLoading(false); setError(t('monitoring.server_codex_inspection_service_unavailable')); return; }
    void loadPageData();
  }, [featureAvailability.checking, featureAvailability.serverCodexInspectionAvailable, loadPageData, managementKey, t]);

  const selectedConfig = useMemo(() => resolveServerAntigravityConfig(managerConfig?.antigravityInspection), [managerConfig?.antigravityInspection]);
  const hasUnsavedChanges = useMemo(() => {
    if (!managerConfig) return false;
    const d = toDraft(managerConfig.antigravityInspection);
    return (Object.keys(d) as AntigravityInspectionDraftField[]).some(k => d[k] !== draft[k]);
  }, [managerConfig, draft]);
  const savedScheduleLabel = formatSchedule(selectedConfig, t);
  const hasRunningRun = runs.some(r => r.status === 'running') || detail?.run.status === 'running';
  const latestRun = runs[0] ?? null;
  const activeRun = detail?.run ?? latestRun;
  const activeTone = getRunTone(activeRun);

  const resultRows = useMemo(() => detail?.results ?? [], [detail?.results]);
  const resultItems = useMemo(() => resultRows.map(item => toServerResultItem(item, t)), [resultRows, t]);
  const resultByKey = useMemo(() => { const m = new Map<string, AntigravityInspectionResult>(); resultRows.forEach(item => m.set(`server-antigravity-${item.id || item.accountKey}`, item)); return m; }, [resultRows]);
  const filteredResultRows = useMemo(() => filterInspectionResults(resultItems, handlingFilter, actionFilter), [actionFilter, handlingFilter, resultItems]);
  const resultPagination = useMemo(() => buildCodexInspectionPaginationState(filteredResultRows, resultPage, resultPageSize), [filteredResultRows, resultPage, resultPageSize]);

  useEffect(() => { setResultPage(1); }, [actionFilter, handlingFilter, detail?.run.id]);

  const scheduleOptions = useMemo(() => [
    { value: 'interval', label: t('monitoring.server_codex_inspection_schedule_interval') },
    { value: 'time_points', label: t('monitoring.server_codex_inspection_schedule_time_points') },
  ], [t]);

  const browserTimeZone = useMemo(detectBrowserTimeZone, []);
  const timeZoneOptions = useMemo(() => {
    const seen = new Set<string>();
    const opts: SelectOption[] = [{ value: '', label: t('monitoring.server_codex_inspection_time_zone_server_default') }];
    const push = (v: string, l: string) => { if (!v || seen.has(v)) return; seen.add(v); opts.push({ value: v, label: l }); };
    if (browserTimeZone && browserTimeZone !== 'UTC') push(browserTimeZone, t('monitoring.server_codex_inspection_time_zone_browser', { tz: browserTimeZone }));
    COMMON_TIME_ZONES.forEach(z => push(z, z));
    return opts;
  }, [browserTimeZone, t]);

  const updateDraft = <K extends AntigravityInspectionDraftField>(key: K, value: ServerAntigravityInspectionDraft[K]) => setDraft(p => ({ ...p, [key]: value }));

  const refreshRuns = useCallback(async (opts?: { silent?: boolean }) => {
    if (!serviceBase) { try { await loadPageData(); } finally {} return; }
    if (!opts?.silent) { setLoading(true); setError(''); }
    try {
      const resp = await usageServiceApi.listAntigravityInspectionRuns(serviceBase, managementKey, RUNS_LIMIT);
      setRuns(resp.items);
      const valid = selectedRunId != null && resp.items.some(r => r.id === selectedRunId);
      if (valid) { if (!opts?.silent || !detail || detail.run.status === 'running') await loadRunDetail(serviceBase, selectedRunId!); }
      else { const fid = resp.items[0]?.id; if (fid) await loadRunDetail(serviceBase, fid); else { setDetail(null); setSelectedRunId(null); } }
    } catch (err: unknown) { if (!opts?.silent) setError(getUsageServiceDisplayError(err, t)); }
    finally { if (!opts?.silent) setLoading(false); }
  }, [detail, loadPageData, loadRunDetail, managementKey, selectedRunId, serviceBase, t]);

  useEffect(() => {
    if (!serviceBase || (!selectedConfig.enabled && !hasRunningRun)) return;
    const timer = window.setInterval(() => { if (saving || running) return; void refreshRuns({ silent: true }); }, 30_000);
    return () => window.clearInterval(timer);
  }, [hasRunningRun, refreshRuns, running, saving, selectedConfig.enabled, serviceBase]);

  const handleSave = async () => {
    if (!serviceBase || !managerConfig) { showNotification(t('monitoring.server_codex_inspection_service_unavailable'), 'warning'); return; }
    const validation = validateDraft(draft, t);
    if (!validation.ok) { showNotification(t('monitoring.server_codex_inspection_config_invalid'), 'warning'); return; }
    setSaving(true);
    try {
      const ai: ManagerAntigravityInspectionConfig = {
        enabled: draft.enabled,
        schedule: draft.scheduleMode === 'time_points'
          ? { mode: 'time_points', timePoints: parseTimePoints(draft.timePoints), intervalMinutes: Number(draft.intervalMinutes) || 60, timeZone: draft.timeZone.trim() }
          : { mode: 'interval', intervalMinutes: Number(draft.intervalMinutes) || 60, timePoints: [], timeZone: draft.timeZone.trim() },
        targetType: validation.values.targetType as string,
        workers: validation.values.workers as number,
        deleteWorkers: validation.values.deleteWorkers as number,
        timeout: validation.values.timeout as number,
        retries: validation.values.retries as number,
        userAgent: draft.userAgent,
        usedPercentThreshold: validation.values.usedPercentThreshold as number,
        sampleSize: validation.values.sampleSize as number,
        autoActionMode: validation.values.autoActionMode as string,
        rateLimitAction: validation.values.rateLimitAction as string,
      };
      const resp = await usageServiceApi.saveManagerConfig(serviceBase, { ...managerConfig, antigravityInspection: ai }, managementKey);
      setManagerConfig(resp.config); setDraft(toDraft(resp.config.antigravityInspection));
      showNotification(t('monitoring.server_antigravity_inspection_config_saved'), 'success');
      setConfigDrawerOpen(false);
    } catch (err: unknown) { showNotification(`${t('notification.save_failed')}: ${getUsageServiceDisplayError(err, t)}`, 'error'); }
    finally { setSaving(false); }
  };

  const executeRun = useCallback(async () => {
    if (!serviceBase) { showNotification(t('monitoring.server_codex_inspection_service_unavailable'), 'warning'); return; }
    setRunning(true); setError('');
    try {
      const d = await usageServiceApi.runAntigravityInspection(serviceBase, managementKey);
      setDetail(d); setSelectedRunId(d.run.id);
      const resp = await usageServiceApi.listAntigravityInspectionRuns(serviceBase, managementKey, RUNS_LIMIT);
      setRuns(resp.items);
      showNotification(t('monitoring.server_antigravity_inspection_run_success'), 'success');
    } catch (err: unknown) { showNotification(`${t('monitoring.server_codex_inspection_run_failed')}: ${getUsageServiceDisplayError(err, t)}`, 'error'); await refreshRuns(); }
    finally { setRunning(false); }
  }, [managementKey, refreshRuns, serviceBase, showNotification, t]);

  const handleRunNow = () => showConfirmation({
    title: t('monitoring.server_antigravity_inspection_run_confirm_title'),
    message: t('monitoring.server_antigravity_inspection_run_confirm_body'),
    confirmText: t('monitoring.server_codex_inspection_run_now'),
    cancelText: t('common.cancel'),
    variant: selectedConfig.autoActionMode === 'delete' ? 'danger' : 'primary',
    onConfirm: executeRun,
  });

  const executeActions = useCallback(async (targets: AntigravityInspectionResult[]) => {
    if (!serviceBase || !detail) return;
    const ids = Array.from(new Set(targets.filter(isActionableServerResult).map(i => i.id)));
    if (ids.length === 0) { showNotification(t('monitoring.server_codex_inspection_no_actions'), 'warning'); return; }
    setExecutingResultIds(new Set(ids));
    try {
      const resp = await usageServiceApi.executeAntigravityInspectionActions(serviceBase, managementKey, detail.run.id, ids);
      setDetail(resp.detail); setSelectedRunId(resp.detail.run.id);
      const runsResp = await usageServiceApi.listAntigravityInspectionRuns(serviceBase, managementKey, RUNS_LIMIT);
      setRuns(runsResp.items);
      const failed = resp.outcomes.filter(o => !o.success);
      showNotification(failed.length > 0 ? t('monitoring.server_codex_inspection_execute_partial', { failed: failed.length, total: resp.outcomes.length }) : t('monitoring.server_codex_inspection_execute_success'), failed.length > 0 ? 'warning' : 'success');
    } catch (err: unknown) { showNotification(`${t('monitoring.server_codex_inspection_execute_failed')}: ${getUsageServiceDisplayError(err, t)}`, 'error'); }
    finally { setExecutingResultIds(new Set()); }
  }, [detail, managementKey, serviceBase, showNotification, t]);

  const handleExecuteActions = useCallback((targets: AntigravityInspectionResult[]) => {
    if (targets.length === 0) return;
    const hasDelete = targets.some(i => i.action === 'delete');
    const first = targets[0];
    showConfirmation({
      title: targets.length === 1 ? t('monitoring.server_codex_inspection_execute_single_title') : t('monitoring.server_codex_inspection_execute_confirm_title'),
      message: targets.length === 1 ? t('monitoring.server_codex_inspection_execute_single_body', { account: first.displayAccount, action: resolveActionLabel(first.action, t) }) : t('monitoring.server_codex_inspection_execute_confirm_body', { total: targets.length, delete: targets.filter(i => i.action === 'delete').length, disable: targets.filter(i => i.action === 'disable').length, enable: 0 }),
      confirmText: targets.length === 1 ? resolveActionLabel(first.action, t) : t('monitoring.server_codex_inspection_execute_all'),
      cancelText: t('common.cancel'),
      variant: hasDelete ? 'danger' : 'primary',
      onConfirm: () => executeActions(targets),
    });
  }, [executeActions, showConfirmation, t]);

  const handleSelectRun = async (runID: number) => {
    if (!serviceBase || runID === selectedRunId) return;
    setSelectedRunId(runID);
    try { await loadRunDetail(serviceBase, runID); }
    catch (err: unknown) { showNotification(getUsageServiceDisplayError(err, t), 'error'); }
  };

  const handleCloseConfigDrawer = useCallback(() => {
    if (hasUnsavedChanges) {
      showConfirmation({
        title: t('monitoring.server_codex_inspection_close_confirm_title'),
        message: t('monitoring.server_codex_inspection_close_unsaved_hint'),
        confirmText: t('monitoring.server_codex_inspection_discard'),
        cancelText: t('common.cancel'),
        variant: 'danger',
        onConfirm: () => { setDraft(toDraft(managerConfig?.antigravityInspection)); setConfigDrawerOpen(false); },
      });
      return;
    }
    setConfigDrawerOpen(false);
  }, [hasUnsavedChanges, managerConfig, showConfirmation, t]);

  const configOverviewItems = buildConfigOverviewItems(selectedConfig as any, { mode: 'server', t, scheduleEnabled: selectedConfig.enabled, scheduleLabel: savedScheduleLabel });

  const handleCopyLogs = useCallback(async (logs: AntigravityInspectionLog[]) => {
    if (!logs.length) return;
    const lines = logs.map(e => { const ts = new Date(e.createdAtMs).toISOString(); const d = e.detail ? ` ${typeof e.detail === 'string' ? e.detail : JSON.stringify(e.detail)}` : ''; return `[${ts}] [${e.level}] ${e.message}${d}`; });
    try { await navigator.clipboard.writeText(lines.join('\n')); showNotification(t('monitoring.server_codex_inspection_logs_copied'), 'success'); }
    catch { showNotification(t('monitoring.server_codex_inspection_logs_copy_failed'), 'error'); }
  }, [showNotification, t]);

  const actionFilterCounts = getActionFilterCounts(resultItems);
  const handlingFilterCounts = countHandlingStates(resultItems);
  const canonicalIds = getCanonicalActionIds(resultRows);
  const executableResults = resultRows.filter(i => canonicalIds.has(i.id));
  const canExecuteActions = detail?.run.status === 'completed';
  const resultsRun = detail?.run ?? null;

  const panelResult: CodexInspectionRunResult | null = resultsRun ? {
    settings: { baseUrl: serviceBase, token: '', targetType: selectedConfig.targetType, workers: selectedConfig.workers, deleteWorkers: selectedConfig.deleteWorkers, timeout: selectedConfig.timeout, retries: selectedConfig.retries, userAgent: selectedConfig.userAgent, usedPercentThreshold: selectedConfig.usedPercentThreshold, sampleSize: selectedConfig.sampleSize },
    files: [],
    results: resultItems,
    summary: { totalFiles: resultsRun.totalFiles, probeSetCount: resultsRun.probeSetCount, sampledCount: resultsRun.sampledCount, disabledCount: resultsRun.disabledCount, enabledCount: resultsRun.enabledCount, deleteCount: resultsRun.deleteCount, disableCount: resultsRun.disableCount, enableCount: resultsRun.enableCount, reauthCount: resultsRun.reauthCount, keepCount: resultsRun.keepCount, usedPercentThreshold: selectedConfig.usedPercentThreshold, sampled: selectedConfig.sampleSize > 0, plannedActionPreview: [] },
    startedAt: resultsRun.startedAtMs,
    finishedAt: resultsRun.finishedAtMs ?? resultsRun.updatedAtMs,
  } : null;

  const filterLabel = (f: ActionFilter) => { switch (f) { case 'delete': return t('monitoring.codex_inspection_filter_delete'); case 'disable': return t('monitoring.codex_inspection_filter_disable'); case 'enable': return t('monitoring.codex_inspection_filter_enable'); case 'reauth': return t('monitoring.codex_inspection_filter_reauth'); case 'keep': return t('monitoring.codex_inspection_action_keep'); default: return t('monitoring.codex_inspection_filter_all'); } };
  const handlingFilterLabel = (f: HandlingFilter) => { switch (f) { case 'pending': return t('monitoring.codex_inspection_handling_filter_pending'); case 'no_action': return t('monitoring.codex_inspection_handling_filter_no_action'); default: return t('monitoring.codex_inspection_handling_filter_all'); } };

  const renderOperation = (item: CodexInspectionResultItem) => {
    const source = resultByKey.get(item.key);
    if (!source) return <span className={styles.primaryReason}>{t('monitoring.codex_inspection_no_action')}</span>;
    return (
      <div className={styles.serverResultOperation}>
        {canonicalIds.has(source.id) ? (
          <Button size="xs" variant={source.action === 'delete' ? 'danger' : 'secondary'} loading={executingResultIds.has(source.id)} disabled={!canExecuteActions || executingResultIds.size > 0} className={styles.serverResultActionButton} onClick={() => handleExecuteActions([source])}>
            {(() => { const Icon = getServerActionIcon(source.action); return <Icon size={13} />; })()}
            {resolveActionLabel(source.action, t)}
          </Button>
        ) : (
          <span className={styles.primaryReason}>{source.action === 'keep' ? t('monitoring.codex_inspection_no_action') : t('monitoring.server_codex_inspection_file_level_action_hint')}</span>
        )}
      </div>
    );
  };

  const fieldErrors = validateInspectionConfigFields(draft as any, t);

  return (
    <div className={styles.page}>
      <CodexInspectionModeTabs activeMode="server" />

      {error ? (
        <div className={styles.topErrorBar} role="alert">
          <span>{error}</span>
          <div className={styles.topErrorActions}><Button variant="secondary" size="sm" onClick={() => void refreshRuns()} loading={loading}>{t('common.retry')}</Button></div>
        </div>
      ) : null}

      {/* Status Panel */}
      <Panel className={styles.statusPanel}>
        <div className={styles.statusBar}>
          <div className={styles.statusInfo}>
            <span className={`${styles.statusBadge} ${statusToneClass[activeTone]}`}><span className={styles.statusDot} />{getRunStatusLabel(activeRun, t)}</span>
            <span className={`${styles.statusBadge} ${selectedConfig.enabled ? statusToneClass.good : statusToneClass.idle}`}><span className={styles.statusDot} />{selectedConfig.enabled ? t('monitoring.server_codex_inspection_schedule_enabled') : t('monitoring.server_codex_inspection_schedule_disabled')}</span>
            <div className={styles.statusMeta}>
              <span>{t('monitoring.server_codex_inspection_last_run')}: {activeRun?.finishedAtMs ? new Date(activeRun.finishedAtMs).toLocaleTimeString(i18n.language) : '--'}</span>
            </div>
          </div>
          <div className={styles.statusActions}>
            <Button variant="secondary" size="sm" onClick={() => void refreshRuns()} loading={loading}>{t('common.refresh')}</Button>
            <Button size="sm" onClick={handleRunNow} loading={running} disabled={!serviceBase || running}>{t('monitoring.server_codex_inspection_run_now')}</Button>
          </div>
        </div>

        <details className={styles.infoNote}>
          <summary>{t('monitoring.server_antigravity_inspection_info_summary')}</summary>
          <ul className={styles.infoNoteList}>
            <li><strong>{t('monitoring.server_codex_inspection_worker_poll')}:</strong> {t('monitoring.server_codex_inspection_effect_hint')}</li>
            <li><strong>{t('monitoring.server_codex_inspection_time_basis')}:</strong> {t('monitoring.server_codex_inspection_server_time_hint')}</li>
          </ul>
        </details>

        <CodexInspectionConfigOverview title={t('monitoring.server_antigravity_inspection_config_title')} editLabel={t('monitoring.codex_inspection_config_overview_edit')} ariaLabel={t('monitoring.server_antigravity_inspection_config_title')} items={configOverviewItems} onEdit={() => setConfigDrawerOpen(true)} />

        <div className={styles.summaryGrid}>
          {[
            { key: 'probe-total', label: t('monitoring.codex_inspection_total_accounts'), value: activeRun ? String(activeRun.probeSetCount) : '--', meta: t('monitoring.server_codex_inspection_total_files', { count: activeRun?.totalFiles ?? 0 }), Icon: IconInbox, accent: 'blue' as const },
            { key: 'sampled', label: t('monitoring.codex_inspection_sampled_accounts'), value: activeRun ? String(activeRun.sampledCount) : '--', meta: formatTrigger(activeRun, t), Icon: IconChartLine, accent: 'cyan' as const },
            { key: 'delete', label: t('monitoring.codex_inspection_delete_count'), value: activeRun ? String(activeRun.deleteCount) : '--', meta: '', tone: 'bad' as const, Icon: IconTrash2, accent: 'red' as const },
            { key: 'disable', label: t('monitoring.codex_inspection_disable_count'), value: activeRun ? String(activeRun.disableCount) : '--', meta: `${t('monitoring.codex_inspection_threshold')}: ${selectedConfig.usedPercentThreshold}%`, tone: 'warn' as const, Icon: IconShield, accent: 'amber' as const },
            { key: 'enable', label: t('monitoring.codex_inspection_enable_count'), value: activeRun ? String(activeRun.enableCount) : '--', meta: t('monitoring.server_codex_inspection_keep_count', { count: activeRun?.keepCount ?? 0 }), tone: 'good' as const, Icon: IconCheck, accent: 'green' as const },
          ].map(card => {
            const Icon = card.Icon;
            return (
              <div key={card.key} className={[styles.summaryCard, summaryAccentClassMap[card.accent], card.tone ? styles[`tone-${card.tone}`] : ''].filter(Boolean).join(' ')}>
                <div className={styles.summaryHeader}><span className={styles.summaryIcon}><Icon size={18} /></span><span className={styles.summaryLabel}>{card.label}</span></div>
                <div className={styles.summaryBody}><strong className={styles.summaryValue}>{card.value}</strong><span className={styles.summaryMeta}>{card.meta}</span></div>
              </div>
            );
          })}
        </div>
      </Panel>

      <div className={styles.serverDetailGrid}>
        {/* Runs History */}
        <Panel title={t('monitoring.server_antigravity_inspection_history_title')} subtitle={t('monitoring.server_antigravity_inspection_history_desc')}>
          {runs.length > 0 ? (
            <div className={styles.runHistoryList} role="tablist">
              {runs.map(run => {
                const tone = getRunTone(run);
                const selected = run.id === selectedRunId;
                return (
                  <button key={run.id} type="button" role="tab" aria-selected={selected} className={`${styles.runHistoryCard} ${selected ? styles.runHistoryCardActive : ''}`} onClick={() => void handleSelectRun(run.id)}>
                    <div className={styles.runHistoryCardHead}><span className={`${styles.statusBadge} ${statusToneClass[tone]}`}><span className={styles.statusDot} />{getRunStatusLabel(run, t)}</span><span className={styles.runHistoryCardId}>#{run.id}</span></div>
                    <div className={styles.runHistoryCardMeta}><span>{formatTimestamp(run.startedAtMs, i18n.language)}</span><span>{formatTrigger(run, t)} · {t('monitoring.codex_inspection_sampled_accounts')}: {run.sampledCount}</span></div>
                    <div className={styles.runHistoryCardActionPills}>
                      {run.deleteCount > 0 && <span className={`${styles.runHistoryCardPill} ${styles.runHistoryCardPillDelete}`}>{t('monitoring.codex_inspection_action_delete')} {run.deleteCount}</span>}
                      {run.disableCount > 0 && <span className={`${styles.runHistoryCardPill} ${styles.runHistoryCardPillDisable}`}>{t('monitoring.codex_inspection_action_disable')} {run.disableCount}</span>}
                      {run.enableCount > 0 && <span className={`${styles.runHistoryCardPill} ${styles.runHistoryCardPillEnable}`}>{t('monitoring.codex_inspection_action_enable')} {run.enableCount}</span>}
                      {run.keepCount > 0 && <span className={`${styles.runHistoryCardPill} ${styles.runHistoryCardPillKeep}`}>{t('monitoring.codex_inspection_action_keep')} {run.keepCount}</span>}
                    </div>
                  </button>
                );
              })}
            </div>
          ) : <div className={styles.emptyBlock}>{t('monitoring.server_antigravity_inspection_history_empty')}</div>}
        </Panel>

        <div className={styles.serverDetailPanels}>
          {detail?.run.error ? <div className={styles.serverError} role="alert">{detail.run.error}</div> : null}

          <CodexInspectionResultsPanel
            result={panelResult}
            filteredResults={resultPagination.pageItems}
            suggestedResults={resultItems.filter(i => i.action !== 'keep')}
            pendingActionCount={executableResults.length}
            manualActionCount={0}
            handlingFilterCounts={handlingFilterCounts}
            filterCounts={actionFilterCounts}
            handlingFilter={handlingFilter}
            actionFilter={actionFilter}
            pagination={resultPagination}
            pageSize={resultPageSize}
            pageSizeOptions={CODEX_INSPECTION_RESULT_PAGE_SIZE_OPTIONS}
            executing={false}
            isInspectionInFlight={Boolean(hasRunningRun)}
            t={t}
            title={t('monitoring.codex_inspection_results_title')}
            subtitle={formatResultsDescription(resultsRun, i18n.language, t)}
            stateHeaderLabel={formatResultStateHeader(resultsRun, t)}
            onActionFilterChange={setActionFilter}
            onHandlingFilterChange={setHandlingFilter}
            onPageChange={setResultPage}
            onPageSizeChange={v => { setResultPageSize(v); setResultPage(1); }}
            onExecutePlanned={() => handleExecuteActions(executableResults)}
            onExecuteSingle={() => undefined}
            onReauthAccount={() => undefined}
            filterLabel={filterLabel}
            handlingFilterLabel={handlingFilterLabel}
            renderOperation={renderOperation}
          />

          {/* Logs Panel */}
          <Panel title={t('monitoring.codex_inspection_logs_title')} subtitle={t('monitoring.server_codex_inspection_logs_desc')}
            extra={
              <div className={styles.logToolbar}>
                <div className={styles.logFilterGroup}>
                  <div className={styles.segmentedControl}>
                    {(['all', 'info', 'success', 'warning', 'error'] as const).map(level => {
                      const counts: Record<string, number> = { all: (detail?.logs ?? []).length, info: 0, success: 0, warning: 0, error: 0 };
                      (detail?.logs ?? []).forEach(e => { if (e.level in counts) counts[e.level]++; });
                      return (
                        <button key={level} type="button" role="tab" aria-selected={logLevelFilter === level} className={`${styles.segmentButton} ${logLevelFilter === level ? styles.segmentButtonActive : ''}`} onClick={() => setLogLevelFilter(level)}>
                          {level === 'all' ? t('monitoring.server_codex_inspection_filter_all') : t(`monitoring.server_codex_inspection_log_level_${level}`)}
                          <span className={styles.segmentCount}>{counts[level]}</span>
                        </button>
                      );
                    })}
                  </div>
                </div>
                <div className={styles.logToolbarRight}>
                  <Button variant="secondary" size="sm" onClick={() => void handleCopyLogs(detail?.logs ?? [])} disabled={(detail?.logs ?? []).length === 0}>{t('monitoring.server_codex_inspection_logs_copy')}</Button>
                  <Button variant="secondary" size="sm" onClick={() => setLogsCollapsed(p => !p)} disabled={(detail?.logs ?? []).length === 0}>{logsCollapsed ? t('monitoring.codex_inspection_expand_logs') : t('monitoring.codex_inspection_fold_logs')}</Button>
                </div>
              </div>
            }
          >
            {!logsCollapsed ? (
              <div className={styles.logList}>
                {(detail?.logs ?? []).filter(e => logLevelFilter === 'all' || e.level === logLevelFilter).map(entry => (
                  <div key={entry.id} className={`${styles.logRow} ${logLevelClass[entry.level] ?? styles.logInfo}`}>
                    <span className={styles.logTime}>{formatTimestamp(entry.createdAtMs, i18n.language)}</span>
                    <span className={styles.logMessage}>{entry.message}{entry.detail ? <small className={styles.serverLogDetail}>{typeof entry.detail === 'string' ? entry.detail : JSON.stringify(entry.detail)}</small> : null}</span>
                  </div>
                ))}
                {(detail?.logs ?? []).length === 0 && <div className={styles.emptyBlockSmall}>{t('monitoring.codex_inspection_logs_empty')}</div>}
              </div>
            ) : <div className={styles.logCollapsedBar}><span>{t('monitoring.codex_inspection_logs_collapsed', { count: (detail?.logs ?? []).length })}</span></div>}
          </Panel>
        </div>
      </div>

      {/* Config Drawer */}
      <InspectionConfigDrawer open={configDrawerOpen} title={t('monitoring.server_antigravity_inspection_config_title')} description={t('monitoring.server_antigravity_inspection_config_desc')} closeLabel={t('common.close')} focusField={null} onClose={handleCloseConfigDrawer}
        footer={
          <>
            <div className={styles.configDrawerStatus}>{hasUnsavedChanges ? <span className={styles.serverUnsavedBadge}>{t('monitoring.server_codex_inspection_unsaved')}</span> : <span>{t('monitoring.server_codex_inspection_saved_applied')}</span>}</div>
            <div className={styles.configDrawerActions}>
              <Button variant="secondary" size="sm" onClick={() => setDraft(toDraft(managerConfig?.antigravityInspection))} disabled={saving || !hasUnsavedChanges}>{t('monitoring.server_codex_inspection_discard')}</Button>
              <Button size="sm" onClick={handleSave} loading={saving} disabled={loading || saving || !hasUnsavedChanges}>{t('monitoring.server_codex_inspection_save_apply')}</Button>
            </div>
          </>
        }
      >
        <section className={styles.configSection}>
          <header className={styles.configSectionHeader}><span>{t('monitoring.server_codex_inspection_config_group_schedule')}</span></header>
          <div className={styles.serverConfigGrid}>
            <div className={`${styles.serverField} ${styles.serverFieldWide}`}><ToggleSwitch checked={draft.enabled} onChange={v => updateDraft('enabled', v)} label={t('monitoring.server_codex_inspection_enable_schedule')} /></div>
            <div className={`${styles.serverField} ${styles.serverFieldWide}`}>
              <span className={styles.serverFieldLabel}>{t('monitoring.server_codex_inspection_schedule_mode')}</span>
              <div className={styles.scheduleSegmented} role="tablist">
                {scheduleOptions.map(opt => (
                  <button key={opt.value} type="button" role="tab" aria-selected={draft.scheduleMode === opt.value} className={`${styles.scheduleSegmentButton} ${draft.scheduleMode === opt.value ? styles.scheduleSegmentButtonActive : ''}`}
                    onClick={() => updateDraft('scheduleMode', isScheduleMode(opt.value) ? opt.value : DEFAULT_SERVER_ANTIGRAVITY_CONFIG.schedule.mode)}>{opt.label}</button>
                ))}
              </div>
            </div>
            {draft.scheduleMode === 'interval' ? (
              <div className={styles.serverField}><Input id="intervalMinutes" label={t('monitoring.server_codex_inspection_interval_minutes')} type="number" min="1" value={draft.intervalMinutes} onChange={e => updateDraft('intervalMinutes', e.target.value)} /></div>
            ) : (
              <>
                <div className={`${styles.serverField} ${styles.serverFieldHalf}`}><Input id="timePoints" label={t('monitoring.server_codex_inspection_time_points')} value={draft.timePoints} onChange={e => updateDraft('timePoints', e.target.value)} placeholder="09:00, 13:30, 22:00" hint={t('monitoring.server_codex_inspection_time_points_hint')} /></div>
                <div className={`${styles.serverField} ${styles.serverFieldHalf}`}><span className={styles.serverFieldLabel}>{t('monitoring.server_codex_inspection_time_zone')}</span><Select value={draft.timeZone} options={timeZoneOptions} onChange={v => updateDraft('timeZone', v)} /></div>
              </>
            )}
          </div>
        </section>
        <InspectionConfigFields draft={draft as any} errors={fieldErrors} t={t} onFieldChange={(f, v) => updateDraft(f as AntigravityInspectionDraftField, v)} onAutoActionModeChange={v => updateDraft('autoActionMode', v)} />
      </InspectionConfigDrawer>
    </div>
  );
}
