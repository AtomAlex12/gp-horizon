// Typed client for the nuxk-core agent API. Types come from
// nuxk-core/api/openapi.yaml via `npm run gen:api` (src/lib/schema.d.ts) —
// the spec is the contract, this file only wires calls to it.
//
// The same UI runs in two places:
//   - served by nuxk-core on the router  → talks to the agent directly;
//   - served by nuxk-controller on the Pi → the controller proxies /api/v1/*
//     to the router's agent and adds /ctl/v1/* (setup, login, history).
//
// A browser logs in with a password and gets an HttpOnly session cookie
// (the router: root from Entware; the controller: admin) — no token in JS.
import type { components } from './schema';

type S = components['schemas'];
export type Status = S['Status'];
export type EngineState = S['EngineState'];
export type EngineInfo = S['EngineInfo'];
export type EngineKind = S['EngineKind'];
export type Health = S['Health'];
export type Probe = S['Probe'];
export type Routing = S['Routing'];
export type NodeInfo = S['NodeInfo'];
export type Metrics = S['Metrics'];
export type LogEntry = S['LogEntry'];
export type PlaneStatus = S['PlaneStatus'];
export type PlaneDesired = S['PlaneDesired'];
export type PlaneList = S['PlaneList'];
export type PlaneOp = S['PlaneOp'];
export type ListMode = S['ListMode'];
export type OnDown = S['OnDown'];
export type Strategy = S['Strategy'];
export type StrategySet = S['StrategySet'];

// nuxk-controller's own API (not part of the agent contract).
export interface AgentState {
  url: string;
  reachable: boolean;
  last_ok?: number;
  last_error?: string;
  info?: NodeInfo;
  controller_version: string;
}
/** GET /ctl/v1/setup — which step of the controller's setup to show. */
export interface SetupState {
  admin: boolean;
  agent: boolean;
  logged_in: boolean;
  agent_url?: string;
}
export interface History {
  wan: string;
  ts: number[];
  wan_rx_bps: number[];
  wan_tx_bps: number[];
  nfq_pps: number[];
  conntrack: number[];
  load1: number[];
  tunnels: Record<string, { rx_bps: number[]; tx_bps: number[] }>;
}

const BASE = import.meta.env.VITE_API_BASE ?? '';
// Dev/stand builds may bake a bearer token in (VITE_API_TOKEN); a real
// install logs in with a password instead.
const TOKEN = import.meta.env.VITE_API_TOKEN ?? '';

export class HttpError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}

function headers(json: boolean): Record<string, string> {
  return {
    ...(json ? { 'Content-Type': 'application/json' } : {}),
    ...(TOKEN ? { Authorization: `Bearer ${TOKEN}` } : {}),
  };
}

export async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: headers(body !== undefined),
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let json: unknown = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    /* non-JSON error page */
  }
  if (!res.ok) {
    const e = (json as S['Error'] | null)?.error;
    throw new HttpError(res.status, e?.code ?? 'error', e?.message ?? res.statusText);
  }
  return json as T;
}

const v1 = (p: string) => `/api/v1${p}`;

export const api = {
  status: () => req<Status>('GET', v1('/status')),
  info: () => req<NodeInfo>('GET', v1('/info')),
  metrics: () => req<Metrics>('GET', v1('/metrics')),
  logs: (after = 0) => req<LogEntry[]>('GET', v1(`/logs?after=${after}&limit=500`)),

  engineAction: (k: EngineKind, action: 'start' | 'stop' | 'restart') =>
    req<S['Ok']>('POST', v1(`/engines/${k}/${action}`)),
  engineProbe: (k: EngineKind) => req<Probe>('POST', v1(`/engines/${k}/probe`)),
  applyRouting: (k: EngineKind, routing: Routing) => req<S['Ok']>('POST', v1(`/engines/${k}/apply`), routing),
  setConfig: (k: EngineKind, cfg: Record<string, string>) => req<S['Ok']>('PUT', v1(`/engines/${k}/config`), cfg),
  strategies: (k: EngineKind) => req<StrategySet>('GET', v1(`/engines/${k}/strategies`)),
  setStrategies: (k: EngineKind, strategies: Strategy[]) => req<S['Ok']>('PUT', v1(`/engines/${k}/strategies`), { strategies }),

  plane: () => req<PlaneStatus>('GET', v1('/plane')),
  planeLists: () => req<PlaneDesired>('GET', v1('/plane/lists')),
  setPlaneLists: (d: PlaneDesired) => req<S['Ok']>('PUT', v1('/plane/lists'), d),
  planeImport: (groups: string[], mode: ListMode) => req<PlaneDesired>('POST', v1('/plane/import'), { groups, mode }),

  // controller only (404 when the UI is served by the agent itself)
  agent: () => req<AgentState>('GET', '/ctl/v1/agent'),
  history: () => req<History>('GET', '/ctl/v1/history'),
  // null on the agent: its web server answers /ctl/* with the page itself
  setup: () => req<SetupState | null>('GET', '/ctl/v1/setup').then((s) => (s && typeof s === 'object' && 'admin' in s ? s : null)),
  setupAdmin: (password: string) => req<{ user: string }>('POST', '/ctl/v1/setup/admin', { password }),
  setupAgent: (url: string, user: string, password: string) =>
    req<AgentState>('POST', '/ctl/v1/setup/agent', { url, user, password }),

  login: (via: 'agent' | 'controller', user: string, password: string) =>
    req<{ user: string }>('POST', via === 'controller' ? '/ctl/v1/auth/login' : v1('/auth/login'), { user, password }),
  logout: (via: 'agent' | 'controller') => req<S['Ok']>('POST', via === 'controller' ? '/ctl/v1/auth/logout' : v1('/auth/logout')),
};

/**
 * Server-Sent Events over fetch — EventSource can't send the Authorization
 * header. Calls onEvent per event until the stream ends or signal aborts.
 */
export async function streamEvents(
  signal: AbortSignal,
  onEvent: (event: string, data: string) => void,
): Promise<void> {
  const res = await fetch(`${BASE}${v1('/events')}`, { headers: headers(false), signal });
  if (!res.ok || !res.body) throw new HttpError(res.status, 'stream', res.statusText);
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) return;
    buf += value;
    let i: number;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, i);
      buf = buf.slice(i + 2);
      let event = 'message';
      const data: string[] = [];
      for (const line of block.split('\n')) {
        if (line.startsWith('event: ')) event = line.slice(7);
        else if (line.startsWith('data: ')) data.push(line.slice(6));
      }
      if (data.length) onEvent(event, data.join('\n'));
    }
  }
}
