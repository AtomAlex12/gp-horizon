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
// --- why a run ended ---------------------------------------------------------
// GP's history says only «ошибка»; the reason is in the run's stderr and its
// last progress. runVerdict reads both: how far the run got, and the stderr
// lines we know, in words. A line we don't know is shown as it is.

export interface RunCause {
  level: 'warn' | 'deg';
  title: string;
  hint?: string;
  line?: string;
}

const KNOWN_ERRORS: { re: RegExp; cause: (status: string) => Omit<RunCause, 'line'> }[] = [
  {
    // the container's /tmp is noexec; fixed by the plugin's own TMPDIR (0.5.0-beta.11)
    re: /gp-root-helper\.\w+\/\S+: Permission denied/,
    cause: () => ({
      level: 'warn',
      title: 'GP не смог запустить свой скрипт проверки',
      hint:
        'GP кладёт скрипт во временную папку, а в контейнере контроллера там запрещён запуск программ. ' +
        'С 0.5.0-beta.11 у GP своя временная папка — обновите контроллер («Система» → «Обновления») и повторите прогон.',
    }),
  },
  {
    re: /managed supervisor exited before target status/,
    cause: (st) =>
      st === 'stopped'
        ? { level: 'deg', title: 'Прогон остановлен', hint: 'Строка в ошибках — след остановки, а не сбой: проверку прервали раньше, чем она вернула итог.' }
        : { level: 'warn', title: 'Проверка прервалась, не вернув итога' },
  },
  {
    re: /root-helper (is not configured|unavailable)/,
    cause: () => ({ level: 'warn', title: 'У GP нет помощника с правами root', hint: 'Переустановите плагин GP в «Плагинах».' }),
  },
];

const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);
const fmt = (v: number) => v.toLocaleString('ru-RU');

export function runVerdict(status: string | null | undefined, log: RunLogTail | null | undefined): { facts: string[]; causes: RunCause[] } {
  const st = status ?? '';
  const failed = st === 'failed' || st === 'timeout';
  const p = (log?.progress ?? {}) as Record<string, unknown>;

  const facts: string[] = [];
  if (failed && typeof p.phase_label === 'string' && p.phase_label) facts.push(`оборвался на этапе «${p.phase_label}»`);
  const done = num(p.attempted) ?? 0,
    total = num(p.attempt_total);
  if (total) facts.push(`проверено ${fmt(done)} из ${fmt(total)} вариантов`);
  const found = num(p.successful);
  if (found !== undefined && total) facts.push(`удачных проверок ${fmt(found)}`);
  // the whole plan at the speed GP measured in this very run
  const ms = num(p.eta_ms_per_attempt),
    par = num(p.eta_parallelism) || 1;
  if (ms && total && total > done && st !== 'success')
    facts.push(
      `при скорости этого прогона (${(ms / 1000).toLocaleString('ru-RU', { maximumFractionDigits: 1 })} с на проверку) весь план занял бы ≈ ${fmtLong(((total - done) * ms) / par / 1000)}`,
    );

  const lines = [log?.stderr_tail, log?.stderr_append]
    .filter(Boolean)
    .join('\n')
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);
  const causes: RunCause[] = [];
  for (const line of lines) {
    const k = KNOWN_ERRORS.find((k) => k.re.test(line));
    if (!k) continue;
    const c = k.cause(st);
    if (!causes.some((x) => x.title === c.title)) causes.push({ ...c, line });
  }
  for (const d of log?.stderr_diagnostics ?? [])
    causes.push({ level: d.severity === 'error' ? 'warn' : 'deg', title: d.label || d.status || 'замечание GP', hint: d.message, line: d.line });
  if (failed && !causes.some((c) => c.level === 'warn')) {
    if (st === 'timeout') causes.push({ level: 'warn', title: 'Вышло время прогона', hint: 'Предел времени — в настройках прогона.' });
    else if (lines.length) causes.push({ level: 'warn', title: 'GP завершил прогон с ошибкой', hint: 'Его последняя строка ошибок:', line: lines[lines.length - 1] });
    else causes.push({ level: 'warn', title: 'GP не записал причину', hint: 'У прогона нет ни журнала, ни ошибок — посмотрите «Журнал работы» плагина GP в «Плагинах».' });
  }
  return { facts, causes };
}

// days for the long ones: a plan of millions of checks runs for weeks
function fmtLong(s: number): string {
  const d = Math.floor(s / 86400),
    h = Math.floor((s % 86400) / 3600),
    m = Math.floor((s % 3600) / 60);
  if (d) return `${fmt(d)} сут ${h} ч`;
  if (h) return `${h} ч ${m} мин`;
  return `${Math.max(1, m)} мин`;
}

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
