// Typed client for nuxk-core /api/v1. Mirrors nuxk-core/api/openapi.yaml.
// The UI has no business logic — it renders these shapes and posts commands.

const BASE = import.meta.env.VITE_API_BASE ?? '';
const TOKEN = import.meta.env.VITE_API_TOKEN ?? '';

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
}

export interface Status {
  version: string;
  ts: number;
  engines: EngineState[];
  plane: Record<string, unknown>;
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
  version: () => req<{ version: string; api: string }>('GET', '/version'),
  status: () => req<Status>('GET', '/status'),

  engines: () => req<EngineState[]>('GET', '/engines'),
  engine: (k: EngineKind) => req<EngineInfo>('GET', `/engines/${k}`),
  engineAction: (k: EngineKind, action: 'start' | 'stop' | 'restart') =>
    req<{ status: string }>('POST', `/engines/${k}/${action}`),
  engineProbe: (k: EngineKind) => req<Probe>('POST', `/engines/${k}/probe`),

  // TODO: lists, decisions, discover, apply, presets, settings, events(SSE)
};

export { HttpError };
