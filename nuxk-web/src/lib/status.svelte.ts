// The live store: platform status (SSE, with a polling fallback), node info,
// metric history and the agent log. One instance for the whole app.
import {
  api,
  streamEvents,
  HttpError,
  type AgentState,
  type History,
  type LogEntry,
  type Metrics,
  type NodeInfo,
  type Status,
} from './api';

export const status = $state<{
  data: Status | null;
  error: string | null;
  loading: boolean;
  needToken: boolean; // 401 — show the token form
  live: boolean; // the SSE stream is up (otherwise polling)
}>({ data: null, error: null, loading: true, needToken: false, live: false });

export const node = $state<{
  via: 'agent' | 'controller' | null; // who serves this UI
  info: NodeInfo | null;
  agent: AgentState | null; // controller mode only
}>({ via: null, info: null, agent: null });

export const history = $state<{ h: History | null }>({ h: null });
export const logs = $state<{ items: LogEntry[] }>({ items: [] });

const LOG_KEEP = 1000;
const LOCAL_POINTS = 120; // agent mode: 10 min at 5 s — the controller keeps 1 h

let started = false;
let abort: AbortController | null = null;
let timers: ReturnType<typeof setTimeout>[] = [];

function fail(e: unknown) {
  status.needToken = e instanceof HttpError && e.status === 401;
  status.error = e instanceof Error ? e.message : String(e);
}

export async function refresh() {
  try {
    status.data = await api.status();
    status.error = null;
    status.needToken = false;
  } catch (e) {
    fail(e);
  }
}

function addLogs(items: LogEntry[]) {
  if (!items.length) return;
  const last = logs.items.length ? logs.items[logs.items.length - 1].seq : 0;
  const fresh = items.filter((e) => e.seq > last);
  if (!fresh.length) return;
  const all = logs.items.concat(fresh);
  logs.items = all.length > LOG_KEEP ? all.slice(all.length - LOG_KEEP) : all;
}

async function detect() {
  try {
    node.agent = await api.agent();
    node.via = 'controller';
    node.info = node.agent.info ?? null;
  } catch (e) {
    if (e instanceof HttpError && e.status === 401) throw e;
    node.via = 'agent';
    node.agent = null;
  }
  if (node.via === 'agent' || !node.info) {
    try {
      node.info = await api.info();
    } catch {
      /* older agent: no /info */
    }
  }
}

// --- SSE with a polling fallback -------------------------------------------

async function streamLoop() {
  for (;;) {
    abort = new AbortController();
    try {
      await streamEvents(abort.signal, (event, data) => {
        if (event === 'status') {
          status.data = JSON.parse(data);
          status.error = null;
          status.live = true;
          status.loading = false;
        } else if (event === 'log') {
          addLogs([JSON.parse(data)]);
        }
      });
    } catch (e) {
      if (abort.signal.aborted) return;
      if (e instanceof HttpError && e.status === 401) {
        fail(e);
        status.loading = false;
        return;
      }
    }
    status.live = false;
    // stream dropped: poll until it comes back
    await refresh();
    await new Promise((r) => timers.push(setTimeout(r, 5000)));
  }
}

// --- metrics: controller history, or rates computed here ---------------------

let prev: Metrics | null = null;

function emptyHistory(): History {
  return { wan: '', ts: [], wan_rx_bps: [], wan_tx_bps: [], nfq_pps: [], conntrack: [], load1: [], tunnels: {} };
}

function addSample(m: Metrics) {
  const p = prev;
  prev = m;
  if (!p || m.ts <= p.ts) return;
  const dt = (m.ts - p.ts) / 1000;
  const rate = (a = 0, b = 0) => (a >= b ? (a - b) / dt : 0);
  const h = history.h ?? emptyHistory();
  const wan = m.wan ?? '';
  h.wan = wan;
  h.ts.push(m.ts);
  const same = wan && wan === p.wan;
  h.wan_rx_bps.push(same ? rate(m.ifaces[wan]?.rx, p.ifaces[wan]?.rx) * 8 : 0);
  h.wan_tx_bps.push(same ? rate(m.ifaces[wan]?.tx, p.ifaces[wan]?.tx) * 8 : 0);
  const pk = (x: Metrics) => x.nfqueues.reduce((a, q) => a + q.packets, 0);
  h.nfq_pps.push(rate(pk(m), pk(p)));
  h.conntrack.push(m.conntrack);
  h.load1.push(m.load1);
  const n = h.ts.length;
  for (const name of Object.keys(m.ifaces)) {
    if (!/^(opkgtun|tun)/.test(name) || !p.ifaces[name]) continue;
    const t = (h.tunnels[name] ??= { rx_bps: Array(n - 1).fill(0), tx_bps: Array(n - 1).fill(0) });
    t.rx_bps.push(rate(m.ifaces[name].rx, p.ifaces[name].rx) * 8);
    t.tx_bps.push(rate(m.ifaces[name].tx, p.ifaces[name].tx) * 8);
  }
  for (const t of Object.values(h.tunnels)) {
    while (t.rx_bps.length < n) t.rx_bps.push(0), t.tx_bps.push(0);
  }
  if (n > LOCAL_POINTS) {
    const cut = n - LOCAL_POINTS;
    for (const k of ['ts', 'wan_rx_bps', 'wan_tx_bps', 'nfq_pps', 'conntrack', 'load1'] as const) h[k].splice(0, cut);
    for (const t of Object.values(h.tunnels)) t.rx_bps.splice(0, cut), t.tx_bps.splice(0, cut);
  }
  history.h = { ...h };
}

async function metricsTick() {
  try {
    if (node.via === 'controller') {
      history.h = await api.history();
      node.agent = await api.agent();
    } else {
      addSample(await api.metrics());
    }
  } catch {
    /* shown by the status error */
  }
  timers.push(setTimeout(metricsTick, 5000));
}

async function infoTick() {
  if (node.via === 'agent') {
    try {
      node.info = await api.info();
    } catch {
      /* keep the last */
    }
  }
  timers.push(setTimeout(infoTick, 60000));
}

export async function startPolling() {
  if (started) return;
  started = true;
  try {
    await detect();
    await refresh();
    if (status.needToken) {
      started = false;
      status.loading = false;
      return;
    }
    addLogs(await api.logs(0).catch(() => []));
  } catch (e) {
    fail(e);
    started = false;
    status.loading = false;
    return;
  }
  status.loading = false;
  void streamLoop();
  void metricsTick();
  timers.push(setTimeout(infoTick, 60000));
}

/** After the token was entered (or cleared). */
export function pollNow() {
  abort?.abort();
  timers.forEach(clearTimeout);
  timers = [];
  started = false;
  status.loading = true;
  void startPolling();
}
