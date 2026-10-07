// The plugin host and the GP plugin, as the controller exposes them:
//   /ctl/v1/plugins*  — install / update / enable, logs, releases
//   /ctl/v1/gp/*      — GP's core API (strategy discovery), token added by
//                       the controller; the browser never talks to GP itself.
// GP's types are generated from its own OpenAPI (see below).
import { req, HttpError } from './api';
import type { components } from './gp-schema';

export type PluginPhase = 'absent' | 'installing' | 'stopped' | 'starting' | 'running' | 'failed';

export interface PluginInfo {
  name: string;
  title: string;
  description: string;
  homepage?: string;
  releases?: string;
  api: string;
  caps: string[];
  listen: string;
  default_version: string;
  enabled: boolean;
  version?: string;
  previous?: string;
  phase: PluginPhase;
  since?: number;
  restarts: number;
  last_error?: string;
  notice?: string;
}

export interface PluginsState {
  host: boolean;
  reason?: string;
  plugins: PluginInfo[];
}

export interface Release {
  tag: string;
  name?: string;
  prerelease: boolean;
  published_at?: string;
  url?: string;
}

export type PluginOp = 'install' | 'enable' | 'disable' | 'restart' | 'rollback';

export const plugins = {
  list: () => req<PluginsState>('GET', '/ctl/v1/plugins'),
  op: (name: string, op: PluginOp, version?: string) =>
    req<{ status: string }>('POST', `/ctl/v1/plugins/${name}/${op}`, op === 'install' ? { version: version ?? '' } : undefined),
  log: (name: string, which: 'run' | 'install') => req<{ log: string }>('GET', `/ctl/v1/plugins/${name}/log?which=${which}`),
  releases: (name: string) => req<Release[]>('GET', `/ctl/v1/plugins/${name}/releases`),
};

// --- GP core ------------------------------------------------------------------
// Types come from GP's own contract, vendored at the version the plugin is
// pinned to: nuxk-controller/plugins/gp/openapi.json (MIT, its LICENSE next
// to it) → gp-schema.d.ts (`npm run gen:gp`; CI checks it's current).

type G = components['schemas'];
export type RunMode = G['RunMode'];
export type RunStatus = G['RunStatus'];
export type StartRun = G['StartRunRequest'];
export type RunSettingsPatch = NonNullable<StartRun['settings']>;
export type ScanLevel = NonNullable<RunSettingsPatch['scan_level']>;
export type CoreStatus = G['CoreStatus'];
export type PreflightStatus = G['PreflightStatus'];
export type PreflightCheck = G['PreflightCheck'];
export type RunProgress = G['RunProgress'];
export type RunLogTail = G['RunLogTail'];
export type RunHistoryItem = G['RunHistoryItem'];
export type StrategyCandidate = G['StrategyCandidate'];
export type CandidatesResponse = G['StrategyCandidatesResponse'];
export type DomainList = G['DomainList'];
export type V2flyCategory = G['V2flyCategory'];
export type V2flyStorage = G['V2flyStorageStatus'];
export type RunSettings = G['RunSettings'];
export type BackupSnapshot = G['BackupSnapshot'];
export type ServiceStatus = G['ServiceStatus'];
export type ServiceActionResult = G['ServiceActionResult'];

const core = (p: string) => `/ctl/v1/gp/${p}`;
const q = (params: Record<string, string | undefined>) => {
  const s = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) s.set(k, v);
  const t = s.toString();
  return t ? `?${t}` : '';
};

/** filters GP's candidates take (and their export) */
export interface CandidateFilter {
  domains?: string[];
  protocol?: string;
  family?: string;
  query?: string;
}
const candidateQuery = (f: CandidateFilter) =>
  q({ domains: f.domains?.join(','), protocol: f.protocol, family: f.family, query: f.query });

export const gp = {
  status: () => req<CoreStatus>('GET', core('status')),
  preflight: () => req<PreflightStatus>('GET', core('strategy-discovery/preflight')),
  progress: () => req<RunProgress>('GET', core('strategy-discovery/current-run-progress')),
  log: () => req<RunLogTail>('GET', core('strategy-discovery/current-run-latest-log')),
  start: (r: StartRun) => req<{ accepted: boolean; run_id: string; status: string }>('POST', core('strategy-discovery/start-run'), r),
  stop: () => req<{ accepted: boolean; run_id: string; status: string }>('POST', core('strategy-discovery/stop-current-run')),
  history: () => req<G['RunHistoryResponse']>('GET', core('runs/history')),
  runLog: (runId: string) => req<RunLogTail>('GET', core('runs/latest-log') + q({ run_id: runId })),
  // GP answers only filtered requests: pass the domains of interest
  candidates: (f: CandidateFilter) => req<CandidatesResponse>('GET', core('strategy-candidates') + candidateQuery(f)),
  /** a link: the browser downloads every matching candidate as NDJSON */
  exportUrl: (f: CandidateFilter) => core('strategy-candidates/export') + candidateQuery(f),

  domainLists: () => req<G['DomainListsResponse']>('GET', core('presets/domain-lists')),
  saveDomainList: (l: G['SaveDomainListRequest']) => req<DomainList>('POST', core('presets/save-domain-list'), l),
  deleteDomainLists: (ids: string[]) => req<G['DeleteResult']>('POST', core('presets/delete-user-domain-list'), { list_ids: ids }),
  v2flyCategories: () => req<G['V2flyCategoriesResponse']>('GET', core('presets/v2fly/categories')),
  v2flyCategory: (name: string) => req<G['V2flyCategoryDomainsResponse']>('GET', core('presets/v2fly/category-domains') + q({ category: name })),

  runSettings: () => req<RunSettings>('GET', core('run-settings')),
  saveRunSettings: (s: RunSettings) => req<RunSettings>('POST', core('run-settings/save'), s),

  backups: () => req<G['BackupsResponse']>('GET', core('backups/list')),
  createBackup: () => req<unknown>('POST', core('backups/create')),
  restoreBackup: (id: string) => req<unknown>('POST', core('backups/restore'), { snapshot_id: id }),
  deleteBackup: (id: string) => req<G['DeleteResult']>('POST', core('backups/delete'), { snapshot_id: id }),
  /** a link: the browser downloads the snapshot's archive */
  backupUrl: (id: string) => core('backups/download-archive') + q({ snapshot_id: id }),
  uploadBackup: async (file: Blob) => {
    const res = await fetch(core('backups/upload'), { method: 'POST', headers: { 'Content-Type': 'application/zip' }, body: file });
    if (!res.ok) {
      let msg = res.statusText;
      try {
        msg = (await res.json())?.error?.message ?? msg;
      } catch {
        /* not JSON */
      }
      throw new HttpError(res.status, 'upload', msg);
    }
  },

  service: () => req<ServiceStatus>('GET', core('service/status')),
  v2flyStorage: () => req<V2flyStorage>('GET', core('service/v2fly/local-storage-status')),
  v2flyCheck: () => req<ServiceActionResult>('POST', core('service/v2fly/check-updates')),
  v2flyUpdate: () => req<ServiceActionResult>('POST', core('service/v2fly/update-local-storage')),
};

export const RUN_STATUS_LABEL: Record<string, string> = {
  idle: 'не идёт',
  queued: 'в очереди',
  running: 'идёт',
  saving: 'сохраняет',
  stopping: 'останавливается',
  stopped: 'остановлен',
  success: 'завершён',
  failed: 'ошибка',
  timeout: 'время вышло',
};
export const RUN_STATUS_CHIP = (s: string) =>
  s === 'success' ? 'ok' : s === 'failed' || s === 'timeout' ? 'warn' : s === 'stopped' ? 'deg' : 'acc';
export const RUN_MODE_LABEL: Record<string, string> = {
  standard: 'каждый домен отдельно',
  multi_domain: 'несколько вместе',
  common_strategy: 'одна общая',
  // the run's kind, when GP's history leaves the mode out
  'standard-discovery': 'каждый домен отдельно',
  'multi-domain-discovery': 'несколько вместе',
  'common-strategy-discovery': 'одна общая',
};
export const LIST_KIND: Record<string, { label: string; chip: string }> = {
  required: { label: 'обязательный', chip: 'acc' },
  desired: { label: 'желательный', chip: '' },
  user: { label: 'свой', chip: 'ok' },
};

export const PHASE_LABEL: Record<PluginPhase, string> = {
  absent: 'не установлен',
  installing: 'устанавливается',
  stopped: 'выключен',
  starting: 'запускается',
  running: 'работает',
  failed: 'сбой',
};
