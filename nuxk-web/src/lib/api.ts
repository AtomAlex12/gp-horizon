// Typed client for nuxk-core /api/v1. Mirrors nuxk-core/api/openapi.yaml.
// The UI has no business logic — it renders these shapes and posts commands.

const BASE = import.meta.env.VITE_API_BASE ?? '';
const TOKEN_KEY = 'nuxk-api-token';

// A token baked in at build time (dev/proto images) wins; otherwise the one the
// user typed into the login form — the router build ships without a token, the
// installer prints it once and it lives in this browser only.
function storedToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? '';
  } catch {
    return '';
  }
}
let TOKEN = import.meta.env.VITE_API_TOKEN || storedToken();

export function setToken(t: string) {
  TOKEN = t.trim();
  try {
    if (TOKEN) localStorage.setItem(TOKEN_KEY, TOKEN);
    else localStorage.removeItem(TOKEN_KEY);
  } catch {
    /* private mode: token lasts for this tab only */
  }
}

export type EngineKind = 'nfqws2' | 'usque' | 'xray';
export type Health = 'ok' | 'degraded' | 'down' | 'unknown';
export type Mode = 'direct' | 'desync' | 'warp' | 'vless';

export interface EngineInfo {
  kind: EngineKind;
  running: boolean;
  pid?: number;
  uptime_sec: number;
  version?: string;
  health: Health;
  iface?: string;
  endpoint?: string;
  routes: number;
  detail?: Record<string, string>;
}

export interface Probe {
  ok: boolean;
  egress_ip?: string;
  rtt_ms?: number;
  detail?: Record<string, string>;
  reason?: string;
  ts: number;
}

// What /status and /engines return: Info plus the last active probe.
export interface EngineState extends EngineInfo {
  probe?: Probe;
  probe_at?: number;
  // Controller intent: true = keep running (auto-restart), false = keep
  // stopped, absent = unmanaged. last_error = last failed action, cleared on success.
  want_run?: boolean;
  last_error?: string;
}

export interface Status {
  version: string;
  ts: number;
  engines: EngineState[];
  plane?: PlaneStatus | null; // absent/null when PLANE is off
}

// --- routing plane (nuxk-core/internal/plane) --------------------------------

export type ListMode = 'desync' | 'warp' | 'vless';
export type OnDown = 'direct' | 'block';

export interface PlaneList {
  name: string;
  mode: ListMode;
  domains: string[];
  source?: string; // manual | imported:<group> | preset:<id>
}

export interface PlaneDesired {
  lists: PlaneList[];
  on_down?: OnDown;
}

export interface PlaneGroup {
  name: string;
  mode: ListMode;
  interface: string;
  block: boolean;
  domains: string[];
}

export interface PlaneOp {
  kind: string;
  group?: string;
  interface?: string;
  block?: boolean;
  domains?: string[];
  groups?: string[];
}

export interface PlaneConflict {
  domain: string;
  group: string;
  user_group: string;
}

export interface PlaneForeign {
  group: string;
  description?: string;
  interface: string;
  domains: string[];
}

export interface PlaneStatus {
  backend: string;
  apply: boolean;
  ifaces: Partial<Record<ListMode, string>>;
  on_down: OnDown;
  lists: PlaneList[] | null;
  groups: PlaneGroup[] | null;
  desync: string[] | null;
  desync_ok: boolean;
  pending: PlaneOp[] | null;
  conflicts?: PlaneConflict[];
  foreign?: PlaneForeign[];
  warnings?: string[];
  last_error?: string;
  checked_at?: number;
  applied_at?: number;
}

// Mirrors engine.Routing (nuxk-core/internal/engine/engine.go).
export interface Routing {
  domains?: string[];
  cidrs?: string[];
  endpoints?: string[];
  strategy?: string;
}

export interface ApiError {
  error: { code: string; message: string };
}

class HttpError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}/api/v1${path}`, {
    method,
    headers: {
      ...(body !== undefined ? { 'Content-Type': 'application/json' } : {}),
      ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  const json = text ? JSON.parse(text) : null;
  if (!res.ok) {
    const e = (json as ApiError | null)?.error;
    throw new HttpError(res.status, e?.code ?? 'error', e?.message ?? res.statusText);
  }
  return json as T;
}

export const api = {
  healthz: () => req<{ status: string }>('GET', '/healthz'),
  version: () => req<{ version: string; commit: string; api: string }>('GET', '/version'),
  status: () => req<Status>('GET', '/status'),

  engines: () => req<EngineState[]>('GET', '/engines'),
  engine: (k: EngineKind) => req<EngineInfo>('GET', `/engines/${k}`),
  engineAction: (k: EngineKind, action: 'start' | 'stop' | 'restart') =>
    req<{ status: string }>('POST', `/engines/${k}/${action}`),
  engineProbe: (k: EngineKind) => req<Probe>('POST', `/engines/${k}/probe`),
  applyRouting: (k: EngineKind, routing: Routing) =>
    req<{ status: string }>('POST', `/engines/${k}/apply`, routing),

  // Only engines implementing engine.Configurable (today: xray) accept this; others 404.
  setConfig: (k: EngineKind, cfg: Record<string, string>) =>
    req<{ status: string }>('PUT', `/engines/${k}/config`, cfg),

  // Routing plane: 404 plane_off when PLANE= is empty in nuxk.conf.
  plane: () => req<PlaneStatus>('GET', '/plane'),
  planeLists: () => req<PlaneDesired>('GET', '/plane/lists'),
  setPlaneLists: (d: PlaneDesired) => req<{ status: string }>('PUT', '/plane/lists', d),
  planeImport: (groups: string[], mode: ListMode) =>
    req<PlaneDesired>('POST', '/plane/import', { groups, mode }),

  // TODO: decisions, discover, presets, events(SSE)
};

export { HttpError };
