import type { AxiosRequestConfig } from 'axios';
import { apiCallApi } from '@/services/api/apiCall';
import { requestCodexUsageRaw } from '@/services/api/codexQuota';
import type { AuthFileItem, CodexRateLimitInfo } from '@/types';
import {
  ANTIGRAVITY_QUOTA_URLS,
  ANTIGRAVITY_REQUEST_HEADERS,
  buildCodexQuotaWindowInfos,
  classifyCodexRateLimitWindows,
  deriveCodexRateLimitUsedPercent,
  formatQuotaResetTime,
  getCodexQuotaWindowUsedPercent,
  isCodexRateLimitReached,
  isDisabledAuthFile,
  normalizePlanType,
  parseAntigravityPayload,
  resolveAntigravityProjectId,
  resolveAuthProvider,
  resolveCodexChatgptAccountId,
  resolveCodexPlanType,
} from '@/utils/quota';
import { normalizeAuthIndex } from '@/utils/usage';
import {
  type CodexInspectionAccount,
  type CodexInspectionLogLevel,
  type CodexInspectionResultItem,
  type CodexInspectionSettings,
} from '@/features/monitoring/codexInspection';
import { readString } from './codexInspectionSettings';

type LogHandler = (level: CodexInspectionLogLevel, message: string) => void;

const QUOTA_BODY_PATTERNS = ['quota exhausted', 'limit reached', 'payment_required'];
const MAX_INSPECTION_ERROR_DETAIL_LENGTH = 2048;

const resolveAntigravityUserAgent = (value: string) => {
  const trimmed = value.trim();
  return trimmed && !trimmed.toLowerCase().includes('codex_cli')
    ? trimmed
    : ANTIGRAVITY_REQUEST_HEADERS['User-Agent'];
};

const readRecord = (value: unknown): Record<string, unknown> | null =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;

const readNestedRecord = (
  record: Record<string, unknown> | null | undefined,
  ...keys: string[]
): Record<string, unknown> | null => {
  if (!record) return null;
  for (const key of keys) {
    const child = readRecord(record[key]);
    if (child) return child;
  }
  return null;
};

const readQuotaString = (record: Record<string, unknown> | null | undefined, ...keys: string[]) => {
  if (!record) return '';
  for (const key of keys) {
    const value = record[key];
    if (value === undefined || value === null) continue;
    const text = String(value).trim();
    if (text) return text;
  }
  return '';
};

const readQuotaNumber = (value: unknown): number | null => {
  if (typeof value === 'number' && Number.isFinite(value)) return value;
  if (typeof value === 'string') {
    const parsed = Number(value.trim().replace(/%$/, ''));
    return Number.isFinite(parsed) ? parsed : null;
  }
  return null;
};

const truncateInspectionDetail = (value: unknown) => {
  const text = readString(value);
  if (!text) return '';
  if (text.length <= MAX_INSPECTION_ERROR_DETAIL_LENGTH) return text;
  return `${text.slice(0, MAX_INSPECTION_ERROR_DETAIL_LENGTH - 3)}...`;
};

const readAuthFileName = (file: AuthFileItem) => {
  const name = readString(file.name);
  if (name) return name;
  const id = readString(file.id);
  if (id) return id;
  const authIndex = normalizeAuthIndex(file['auth_index'] ?? file.authIndex);
  return authIndex || 'unknown-auth-file';
};

const readDisplayAccount = (file: AuthFileItem) =>
  readString(file.account) ||
  readString(file.email) ||
  readString(file.label) ||
  readString(file.name) ||
  readString(file.id) ||
  normalizeAuthIndex(file['auth_index'] ?? file.authIndex) ||
  '-';

export const toInspectionAccount = (file: AuthFileItem): CodexInspectionAccount => ({
  key: `${readAuthFileName(file)}::${normalizeAuthIndex(file['auth_index'] ?? file.authIndex) || '-'}`,
  fileName: readAuthFileName(file),
  displayAccount: readDisplayAccount(file),
  authIndex: normalizeAuthIndex(file['auth_index'] ?? file.authIndex),
  accountId: resolveCodexChatgptAccountId(file),
  provider: resolveAuthProvider(file),
  disabled: isDisabledAuthFile(file),
  status: readString(file.status),
  state: readString(file.state),
  raw: file,
});

const withRetry = async <T>(retries: number, task: () => Promise<T>): Promise<T> => {
  let lastError: unknown;

  for (let attempt = 0; attempt <= retries; attempt += 1) {
    try {
      return await task();
    } catch (error) {
      lastError = error;
    }
  }

  throw lastError;
};

type CodexInspectionDecision = Pick<
  CodexInspectionResultItem,
  'action' | 'actionReason' | 'usedPercent' | 'isQuota'
>;

type UnauthorizedReason = 'unknown' | 'expired' | 'invalidated';

const isDeactivatedWorkspaceResponse = (statusCode: number, bodyText: string): boolean =>
  statusCode === 402 && bodyText.toLowerCase().includes('deactivated_workspace');

const resolveDeactivatedWorkspaceProbeAction = (
  usedPercent: number | null
): CodexInspectionDecision => ({
  action: 'delete',
  actionReason: '接口返回 402，工作区已停用，建议删除账号',
  usedPercent,
  isQuota: false,
});

const classifyUnauthorizedReason = (bodyText: string): UnauthorizedReason => {
  const normalized = bodyText.trim().toLowerCase();
  if (
    normalized.includes('provided authentication token is expired') ||
    normalized.includes('authentication token is expired') ||
    normalized.includes('token is expired')
  ) {
    return 'expired';
  }
  if (
    normalized.includes('authentication token has been invalidated') ||
    normalized.includes('token has been invalidated') ||
    normalized.includes('token is invalidated')
  ) {
    return 'invalidated';
  }
  return 'unknown';
};

const resolveUnauthorizedProbeAction = (
  bodyText: string,
  usedPercent: number | null
): CodexInspectionDecision => {
  switch (classifyUnauthorizedReason(bodyText)) {
    case 'expired':
      return {
        action: 'reauth',
        actionReason: '接口返回 401，登录已过期，建议重新登录账号',
        usedPercent,
        isQuota: false,
      };
    case 'invalidated':
      return {
        action: 'reauth',
        actionReason: '接口返回 401，认证令牌已失效，建议重新登录账号',
        usedPercent,
        isQuota: false,
      };
    default:
      return {
        action: 'reauth',
        actionReason: '接口返回 401，认证失败，建议重新登录账号',
        usedPercent,
        isQuota: false,
      };
  }
};

const resolveLegacyProbeAction = (
  account: CodexInspectionAccount,
  statusCode: number,
  bodyText: string,
  usedPercent: number | null,
  isQuota: boolean,
  threshold: number
): CodexInspectionDecision => {
  const overThreshold = usedPercent !== null && usedPercent >= threshold;
  if (statusCode === 401) {
    return resolveUnauthorizedProbeAction(bodyText, usedPercent);
  }
  if (isQuota || overThreshold) {
    if (account.disabled) {
      return {
        action: 'keep',
        actionReason: overThreshold ? '额度超阈值，但账号已禁用' : '额度已耗尽，但账号已禁用',
        usedPercent,
        isQuota,
      };
    }
    return {
      action: 'disable',
      actionReason: overThreshold ? '额度超阈值，建议禁用账号' : '额度已耗尽，建议禁用账号',
      usedPercent,
      isQuota,
    };
  }
  if (statusCode === 200 && account.disabled) {
    return {
      action: 'enable',
      actionReason: '账号恢复健康，建议重新启用',
      usedPercent,
      isQuota: false,
    };
  }
  return {
    action: 'keep',
    actionReason: '无需处理',
    usedPercent,
    isQuota: false,
  };
};

const resolveWindowAwareProbeAction = (
  account: CodexInspectionAccount,
  statusCode: number,
  bodyText: string,
  rateLimit: CodexRateLimitInfo | null,
  threshold: number,
  planType?: string | null
): CodexInspectionDecision | null => {
  if (!rateLimit) return null;

  const { fiveHourWindow, weeklyWindow, monthlyWindow, longWindow } =
    classifyCodexRateLimitWindows(rateLimit, {
      teamPlan: normalizePlanType(planType) === 'team',
    });
  const longWindowUsedPercent = getCodexQuotaWindowUsedPercent(longWindow);
  if (!longWindow || longWindowUsedPercent === null) return null;

  const fiveHourUsedPercent = getCodexQuotaWindowUsedPercent(fiveHourWindow);
  const longWindowLabel =
    longWindow === weeklyWindow ? '周额度' : longWindow === monthlyWindow ? '月额度' : '长期额度';
  const longWindowOverThreshold = longWindowUsedPercent >= threshold;
  const fiveHourOverThreshold = fiveHourUsedPercent !== null && fiveHourUsedPercent >= threshold;

  if (statusCode === 401) {
    return resolveUnauthorizedProbeAction(bodyText, longWindowUsedPercent);
  }

  if (longWindowOverThreshold) {
    if (account.disabled) {
      return {
        action: 'keep',
        actionReason: `${longWindowLabel}达到阈值，但账号已禁用`,
        usedPercent: longWindowUsedPercent,
        isQuota: true,
      };
    }
    return {
      action: 'disable',
      actionReason: `${longWindowLabel}达到阈值，建议禁用账号`,
      usedPercent: longWindowUsedPercent,
      isQuota: true,
    };
  }

  if (account.disabled) {
    return {
      action: 'enable',
      actionReason: fiveHourOverThreshold
        ? `5 小时额度达到阈值，但${longWindowLabel}仍可用，建议立即启用账号`
        : `${longWindowLabel}仍可用，建议立即启用账号`,
      usedPercent: longWindowUsedPercent,
      isQuota: false,
    };
  }

  if (fiveHourOverThreshold) {
    return {
      action: 'keep',
      actionReason: `5 小时额度达到阈值，但${longWindowLabel}仍可用，暂不禁用账号`,
      usedPercent: longWindowUsedPercent,
      isQuota: false,
    };
  }

  return {
    action: 'keep',
    actionReason: `${longWindowLabel}仍可用，无需处理`,
    usedPercent: longWindowUsedPercent,
    isQuota: false,
  };
};

const resolveProbeAction = (
  account: CodexInspectionAccount,
  statusCode: number,
  bodyText: string,
  rateLimit: CodexRateLimitInfo | null,
  usedPercent: number | null,
  isQuota: boolean,
  threshold: number,
  planType?: string | null
): CodexInspectionDecision => {
  if (isDeactivatedWorkspaceResponse(statusCode, bodyText)) {
    return resolveDeactivatedWorkspaceProbeAction(usedPercent);
  }

  const windowAwareDecision = resolveWindowAwareProbeAction(
    account,
    statusCode,
    bodyText,
    rateLimit,
    threshold,
    planType
  );
  if (windowAwareDecision) return windowAwareDecision;
  return resolveLegacyProbeAction(account, statusCode, bodyText, usedPercent, isQuota, threshold);
};

const parseAntigravityRemainingFraction = (value: unknown): number | null => {
  const parsed = readQuotaNumber(value);
  if (parsed === null) return null;
  const fraction = parsed > 1 && parsed <= 100 ? parsed / 100 : parsed;
  return Math.max(0, Math.min(1, fraction));
};

const getAntigravityRemainingFraction = (entry: Record<string, unknown>): number | null => {
  const quotaInfo = readNestedRecord(entry, 'quotaInfo', 'quota_info');
  return parseAntigravityRemainingFraction(
    quotaInfo?.remainingFraction ??
      quotaInfo?.remaining_fraction ??
      quotaInfo?.remaining ??
      entry.remainingFraction ??
      entry.remaining_fraction ??
      entry.remaining
  );
};

const getAntigravityResetTime = (entry: Record<string, unknown>) => {
  const quotaInfo = readNestedRecord(entry, 'quotaInfo', 'quota_info');
  return (
    readQuotaString(quotaInfo, 'resetTime', 'reset_time') ||
    readQuotaString(entry, 'resetTime', 'reset_time')
  );
};

const isAntigravityClaudeModel = (id: string, entry: Record<string, unknown>) => {
  const searchText = [
    id,
    readQuotaString(entry, 'displayName', 'display_name'),
    readQuotaString(entry, 'model'),
    readQuotaString(entry, 'apiProvider', 'api_provider'),
    readQuotaString(entry, 'modelProvider', 'model_provider'),
  ]
    .join(' ')
    .toLowerCase();
  return (
    (searchText.includes('claude') || searchText.includes('anthropic')) &&
    !searchText.includes('gemini')
  );
};

const toStableQuotaId = (value: string, fallback: string) => {
  const normalized = value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '');
  return normalized || fallback;
};

const buildAntigravityClaudeQuotaWindowsFromModels = (
  models: Record<string, unknown> | null
): CodexInspectionResultItem['quotaWindows'] => {
  if (!models) return [];
  return Object.entries(models)
    .map(([modelId, rawEntry], index) => {
      const entry = readRecord(rawEntry);
      if (!entry || !isAntigravityClaudeModel(modelId, entry)) return null;
      const remainingFraction = getAntigravityRemainingFraction(entry);
      if (remainingFraction === null) return null;
      const usedPercent = Math.max(0, Math.min(100, (1 - remainingFraction) * 100));
      const label =
        readQuotaString(entry, 'displayName', 'display_name', 'model') || modelId;
      return {
        id: `antigravity-claude-${toStableQuotaId(modelId, `model-${index + 1}`)}`,
        labelKey: 'antigravity_quota.claude_model',
        labelParams: { name: label },
        usedPercent,
        resetLabel: formatQuotaResetTime(getAntigravityResetTime(entry)) || '-',
        limitWindowSeconds: null,
      };
    })
    .filter((window): window is NonNullable<CodexInspectionResultItem['quotaWindows']>[number] =>
      Boolean(window)
    )
    .sort((left, right) => left.id.localeCompare(right.id));
};

const buildAntigravityClaudeQuotaWindowsFromGroups = (
  groups: unknown
): CodexInspectionResultItem['quotaWindows'] => {
  if (!Array.isArray(groups)) return [];
  return groups.flatMap((rawGroup, groupIndex) => {
    const group = readRecord(rawGroup);
    if (!group) return [];
    const groupLabel =
      readQuotaString(group, 'displayName', 'display_name') || `Claude ${groupIndex + 1}`;
    const groupSearchText = `${groupLabel} ${readQuotaString(group, 'description')}`.toLowerCase();
    if (!groupSearchText.includes('claude') && !groupSearchText.includes('anthropic')) return [];
    const buckets = group.buckets;
    if (!Array.isArray(buckets)) return [];
    return buckets
      .map((rawBucket, bucketIndex) => {
        const bucket = readRecord(rawBucket);
        if (!bucket) return null;
        const remainingFraction = getAntigravityRemainingFraction(bucket);
        if (remainingFraction === null) return null;
        const usedPercent = Math.max(0, Math.min(100, (1 - remainingFraction) * 100));
        const bucketId =
          readQuotaString(bucket, 'bucketId', 'bucket_id') || `bucket-${bucketIndex + 1}`;
        const bucketLabel =
          readQuotaString(bucket, 'displayName', 'display_name') || groupLabel;
        return {
          id: `antigravity-claude-${toStableQuotaId(groupLabel, `group-${groupIndex + 1}`)}-${toStableQuotaId(bucketId, `bucket-${bucketIndex + 1}`)}`,
          labelKey: 'antigravity_quota.claude_window',
          labelParams: { name: bucketLabel },
          usedPercent,
          resetLabel:
            formatQuotaResetTime(
              readQuotaString(bucket, 'resetTime', 'reset_time') ||
                readQuotaString(group, 'resetTime', 'reset_time')
            ) || '-',
          limitWindowSeconds: null,
        };
      })
      .filter((window): window is NonNullable<CodexInspectionResultItem['quotaWindows']>[number] =>
        Boolean(window)
      );
  });
};

const buildAntigravityClaudeQuotaWindows = (
  payload: Record<string, unknown> | null
): CodexInspectionResultItem['quotaWindows'] => {
  if (!payload) return [];
  const modelWindows = buildAntigravityClaudeQuotaWindowsFromModels(readNestedRecord(payload, 'models'));
  if (modelWindows.length > 0) return modelWindows;
  return buildAntigravityClaudeQuotaWindowsFromGroups(payload.groups);
};

const maxQuotaWindowUsedPercent = (
  windows: CodexInspectionResultItem['quotaWindows']
): number | null => {
  const values = (windows ?? [])
    .map((window) => window.usedPercent)
    .filter((value): value is number => typeof value === 'number' && Number.isFinite(value));
  return values.length > 0 ? Math.max(...values) : null;
};

const resolveAntigravityProbeAction = (
  account: CodexInspectionAccount,
  statusCode: number,
  bodyText: string,
  usedPercent: number | null,
  threshold: number,
  hasQuotaData: boolean
): CodexInspectionDecision => {
  if (statusCode === 401) return resolveUnauthorizedProbeAction(bodyText, usedPercent);
  if (statusCode < 200 || statusCode >= 300) {
    return {
      action: 'keep',
      actionReason: 'Antigravity Claude 额度响应异常，保留账号',
      usedPercent,
      isQuota: false,
    };
  }
  if (!hasQuotaData) {
    return {
      action: 'keep',
      actionReason: '未找到 Claude 模型额度，已忽略 Gemini 额度',
      usedPercent,
      isQuota: false,
    };
  }
  if (usedPercent !== null && usedPercent >= threshold) {
    if (account.disabled) {
      return {
        action: 'keep',
        actionReason: 'Antigravity Claude 额度达到阈值，但账号已禁用',
        usedPercent,
        isQuota: true,
      };
    }
    return {
      action: 'disable',
      actionReason: 'Antigravity Claude 额度达到阈值，建议禁用账号',
      usedPercent,
      isQuota: true,
    };
  }
  if (account.disabled) {
    return {
      action: 'enable',
      actionReason: 'Antigravity Claude 额度仍可用，建议重新启用',
      usedPercent,
      isQuota: false,
    };
  }
  return {
    action: 'keep',
    actionReason: 'Antigravity Claude 额度仍可用，无需处理',
    usedPercent,
    isQuota: false,
  };
};

const requestAntigravityQuotaRaw = async (
  account: CodexInspectionAccount,
  settings: CodexInspectionSettings,
  requestConfig: AxiosRequestConfig
) => {
  const authIndex = account.authIndex ?? '';
  const projectId = await resolveAntigravityProjectId(account.raw);
  let lastResult: Awaited<ReturnType<typeof apiCallApi.request>> | null = null;
  let fallbackResult: Awaited<ReturnType<typeof apiCallApi.request>> | null = null;
  let lastError: unknown;

  for (const url of ANTIGRAVITY_QUOTA_URLS) {
    try {
      const result = await apiCallApi.request(
        {
          authIndex,
          method: 'POST',
          url,
          header: {
            ...ANTIGRAVITY_REQUEST_HEADERS,
            'User-Agent': resolveAntigravityUserAgent(settings.userAgent),
          },
          data: JSON.stringify({ project: projectId }),
        },
        requestConfig
      );
      lastResult = result;
      if (result.statusCode >= 200 && result.statusCode < 300) {
        const payload = parseAntigravityPayload(result.body ?? result.bodyText);
        if (buildAntigravityClaudeQuotaWindows(payload).length > 0) {
          return { result, payload };
        }
        fallbackResult ??= result;
        continue;
      }
      if (result.statusCode !== 403 && result.statusCode !== 404) {
        return { result, payload: parseAntigravityPayload(result.body ?? result.bodyText) };
      }
    } catch (error) {
      lastError = error;
    }
  }

  if (fallbackResult) {
    return {
      result: fallbackResult,
      payload: parseAntigravityPayload(fallbackResult.body ?? fallbackResult.bodyText),
    };
  }
  if (lastResult) {
    return {
      result: lastResult,
      payload: parseAntigravityPayload(lastResult.body ?? lastResult.bodyText),
    };
  }
  throw lastError ?? new Error('Antigravity Claude quota request failed');
};

const inspectAntigravityAccount = async (
  account: CodexInspectionAccount,
  settings: CodexInspectionSettings,
  requestConfig: AxiosRequestConfig,
  onLog?: LogHandler
): Promise<CodexInspectionResultItem> => {
  const { result, payload } = await withRetry(settings.retries, () =>
    requestAntigravityQuotaRaw(account, settings, requestConfig)
  );

  const quotaWindows = buildAntigravityClaudeQuotaWindows(payload);
  if (!result.hasStatusCode) {
    onLog?.('warning', `${account.displayAccount} Antigravity Claude 额度探测未返回 status_code，保留账号`);
    return {
      ...account,
      action: 'keep',
      actionReason: '探测响应缺少 status_code，保留账号',
      statusCode: null,
      usedPercent: null,
      isQuota: false,
      error: '响应缺少 status_code',
      planType: 'claude',
      quotaWindows,
      errorKind: 'missing_status',
      errorDetail: truncateInspectionDetail(result.bodyText) || '响应缺少 status_code',
    };
  }

  const usedPercent = maxQuotaWindowUsedPercent(quotaWindows);
  const decision = resolveAntigravityProbeAction(
    account,
    result.statusCode,
    result.bodyText,
    usedPercent,
    settings.usedPercentThreshold,
    quotaWindows.length > 0
  );
  const successLevel =
    decision.action === 'disable' || decision.action === 'reauth'
      ? 'warning'
      : decision.action === 'enable'
        ? 'success'
        : 'info';
  const percentText = decision.usedPercent === null ? '--' : `${decision.usedPercent.toFixed(1)}%`;
  onLog?.(
    successLevel,
    `${account.displayAccount} -> ${decision.action} (Antigravity Claude · HTTP ${result.statusCode} · 已用 ${percentText})`
  );

  return {
    ...account,
    action: decision.action,
    actionReason: decision.actionReason,
    statusCode: result.statusCode,
    usedPercent: decision.usedPercent,
    isQuota: decision.isQuota,
    error: '',
    planType: 'claude',
    quotaWindows,
    errorKind:
      result.statusCode >= 200 && result.statusCode < 300
        ? quotaWindows.length === 0
          ? 'empty_quota'
          : ''
        : 'http_status',
    errorDetail:
      result.statusCode >= 200 && result.statusCode < 300
        ? quotaWindows.length === 0
          ? 'Antigravity 响应中未找到 Claude 模型额度，已忽略 Gemini 额度'
          : ''
        : truncateInspectionDetail(result.bodyText),
  };
};

export const inspectSingleAccount = async (
  account: CodexInspectionAccount,
  settings: CodexInspectionSettings,
  onLog?: LogHandler
): Promise<CodexInspectionResultItem> => {
  if (!account.authIndex) {
    onLog?.('warning', `${account.displayAccount} 缺少 auth_index，跳过探测`);
    return {
      ...account,
      action: 'keep',
      actionReason: '缺少 auth_index，保留账号',
      statusCode: null,
      usedPercent: null,
      isQuota: false,
      error: '缺少 auth_index',
      planType: resolveCodexPlanType(account.raw),
      quotaWindows: [],
      errorKind: 'missing_auth_index',
      errorDetail: '缺少 auth_index',
    };
  }

  const authIndex = account.authIndex;
  const requestConfig: AxiosRequestConfig =
    settings.timeout > 0 ? { timeout: settings.timeout } : {};

  try {
    if (settings.targetType === 'antigravity' || account.provider === 'antigravity') {
      return await inspectAntigravityAccount(account, settings, requestConfig, onLog);
    }

    const { result, payload } = await withRetry(settings.retries, () =>
      requestCodexUsageRaw({
        authIndex,
        accountId: account.accountId,
        userAgent: settings.userAgent,
        requestConfig,
      })
    );

    const planType =
      normalizePlanType(payload?.plan_type ?? payload?.planType) ?? resolveCodexPlanType(account.raw);
    const quotaWindows = payload ? buildCodexQuotaWindowInfos(payload, { planType }) : [];

    if (!result.hasStatusCode) {
      onLog?.('warning', `${account.displayAccount} 探测未返回 status_code，保留账号`);
      const errorDetail =
        truncateInspectionDetail(result.bodyText) || '探测响应缺少 status_code';
      return {
        ...account,
        action: 'keep',
        actionReason: '探测响应缺少 status_code，保留账号',
        statusCode: null,
        usedPercent: null,
        isQuota: false,
        error: '响应缺少 status_code',
        planType,
        quotaWindows,
        errorKind: 'missing_status',
        errorDetail,
      };
    }

    const rateLimit = payload?.rate_limit ?? payload?.rateLimit ?? null;
    const usedPercent = deriveCodexRateLimitUsedPercent(rateLimit);
    const bodyText = result.bodyText.toLowerCase();
    const isQuota =
      result.statusCode === 402 ||
      QUOTA_BODY_PATTERNS.some((pattern) => bodyText.includes(pattern)) ||
      isCodexRateLimitReached(rateLimit) ||
      (usedPercent !== null && usedPercent >= settings.usedPercentThreshold);
    const decision = resolveProbeAction(
      account,
      result.statusCode,
      result.bodyText,
      rateLimit,
      usedPercent,
      isQuota,
      settings.usedPercentThreshold,
      planType
    );

    const successLevel =
      decision.action === 'delete'
        ? 'error'
        : decision.action === 'disable'
          ? 'warning'
          : decision.action === 'enable'
            ? 'success'
            : 'info';
    const percentText =
      decision.usedPercent === null ? '--' : `${decision.usedPercent.toFixed(1)}%`;
    onLog?.(
      successLevel,
      `${account.displayAccount} -> ${decision.action} (HTTP ${result.statusCode} · 已用 ${percentText})`
    );

    return {
      ...account,
      action: decision.action,
      actionReason: decision.actionReason,
      statusCode: result.statusCode,
      usedPercent: decision.usedPercent,
      isQuota: decision.isQuota,
      error: '',
      planType,
      quotaWindows,
      errorKind:
        result.statusCode >= 200 && result.statusCode < 300 ? '' : 'http_status',
      errorDetail:
        result.statusCode >= 200 && result.statusCode < 300
          ? ''
          : truncateInspectionDetail(result.bodyText),
    };
  } catch (error) {
    const errorMessage = error instanceof Error ? error.message : String(error || '探测失败');
    const errorDetail = truncateInspectionDetail(errorMessage) || '探测失败';
    onLog?.('warning', `${account.displayAccount} 探测异常，保留账号：${errorMessage}`);
    return {
      ...account,
      action: 'keep',
      actionReason: '探测异常，保留账号',
      statusCode: null,
      usedPercent: null,
      isQuota: false,
      error: errorMessage,
      planType: resolveCodexPlanType(account.raw),
      quotaWindows: [],
      errorKind: 'request_error',
      errorDetail,
    };
  }
};
