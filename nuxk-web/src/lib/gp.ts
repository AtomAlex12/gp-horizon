// The plugin host and the GP plugin, as the controller exposes them:
//   /ctl/v1/plugins*  — install / update / enable, logs, releases
//   /ctl/v1/gp/*      — GP's core API (strategy discovery), token added by
//                       the controller; the browser never talks to GP itself.
// GP's types below follow its OpenAPI (GP Control Plane API 0.4.2) for the
// calls the UI makes; they are written here, not generated, until GP's repo
// carries a license that lets us vendor its openapi.json.
import { req } from './api';

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

export type RunMode = 'standard' | 'multi_domain' | 'common_strategy';
export type RunStatus = 'idle' | 'queued' | 'running' | 'saving' | 'stopping' | 'stopped' | 'success' | 'failed' | 'timeout';
export type ScanLevel = 'quick' | 'standard' | 'force';

export interface CoreStatus {
  state: 'idle' | 'running' | 'stopping' | 'degraded' | 'error';
  current_run?: { run_id: string; status: RunStatus } | null;
  updated_at?: string;
}

export interface PreflightCheck {
  name: string;
  status: string;
  message?: string;
}
export interface PreflightStatus {
  ready: boolean;
  checks: PreflightCheck[];
}

export interface RunProgress {
  run_id?: string;
  status: RunStatus;
  stage?: string;
  domains_total?: number;
  domains_processed?: number;
  attempts_total?: number;
  attempts_processed?: number;
  strategies_total?: number;
  strategies_processed?: number;
  elapsed_seconds?: number;
  eta_seconds?: number;
}

export interface RunLogTail {
  run_id?: string | null;
  status?: string | null;
  stdout_tail: string;
  stderr_tail: string;
}

export interface StartRun {
  mode: RunMode;
  domains: string[];
  protocols: ('tcp' | 'quic')[];
  settings: {
    scan_level: ScanLevel;
    enable_http: boolean;
    enable_tls12: boolean;
    enable_tls13: boolean;
    include_quic: boolean;
  };
}

export interface RunHistoryItem {
  run_id: string;
  kind?: string;
  mode?: RunMode;
  status: RunStatus;
  started_at: string;
  completed_at?: string;
  domains?: string[];
  summary?: Record<string, unknown>;
}

export interface CandidateSeen {
  run_id?: string;
  domain: string;
  test?: string;
  ip_version?: string;
  seen_at?: string;
}

export interface StrategyCandidate {
  id: string;
  protocol: string;
  args: string;
  status: string;
  first_seen_at?: string;
  last_seen_at?: string;
  fragmentation_safe?: boolean;
  fragmentation_reason?: string;
  family?: string;
  family_reason?: string;
  seen?: CandidateSeen[];
  common_seen?: { domains: string[] }[];
}

export interface CandidatesResponse {
  candidates: StrategyCandidate[];
  total: number;
}

const core = (p: string) => `/ctl/v1/gp/${p}`;

export const gp = {
  status: () => req<CoreStatus>('GET', core('status')),
  preflight: () => req<PreflightStatus>('GET', core('strategy-discovery/preflight')),
  progress: () => req<RunProgress>('GET', core('strategy-discovery/current-run-progress')),
  log: () => req<RunLogTail>('GET', core('strategy-discovery/current-run-latest-log')),
  start: (r: StartRun) => req<{ accepted: boolean; run_id: string; status: string }>('POST', core('strategy-discovery/start-run'), r),
  stop: () => req<{ accepted: boolean; run_id: string; status: string }>('POST', core('strategy-discovery/stop-current-run')),
  history: () => req<{ runs: RunHistoryItem[] }>('GET', core('runs/history')),
  // GP answers only filtered requests: pass the domains of interest
  candidates: (domains: string[]) =>
    req<CandidatesResponse>('GET', core('strategy-candidates') + `?domains=${encodeURIComponent(domains.join(','))}`),
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

export const PHASE_LABEL: Record<PluginPhase, string> = {
  absent: 'не установлен',
  installing: 'устанавливается',
  stopped: 'выключен',
  starting: 'запускается',
  running: 'работает',
  failed: 'сбой',
};
