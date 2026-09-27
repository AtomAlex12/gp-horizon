// New releases and the router's update from the panel (GET /api/v1/update).
// One store for the header's «доступна X» and the card in «Система».
//
// The update restarts the agent: for a few seconds its API is gone. While a
// run is going, the store asks every 2 s and takes a failed request for that
// restart, not for an error.
import { api, type UpdateSettings, type UpdateStatus } from './api';
import { node } from './status.svelte';

export const upd = $state<{
  s: UpdateStatus | null;
  err: string;
  busy: '' | 'check' | 'start' | 'settings';
  restarting: boolean; // a run is going and the agent doesn't answer: it's restarting
  startedHere: boolean; // this tab started the run: reload the page when it's done
  reloading: boolean; // done, the new page loads in a moment
}>({ s: null, err: '', busy: '', restarting: false, startedHere: false, reloading: false });

export const running = () => upd.s?.run?.state === 'running';

let timer: ReturnType<typeof setTimeout> | null = null;
let started = false;

function schedule() {
  if (timer) clearTimeout(timer);
  // a run going: every 2 s; an agent that hasn't looked yet (just started,
  // it looks within two minutes): every 10 s; otherwise every half hour
  const unchecked = !!upd.s?.settings.check && !upd.s.checked_at;
  timer = setTimeout(load, running() || upd.startedHere ? 2000 : unchecked ? 10_000 : 30 * 60_000);
}

export async function load() {
  try {
    upd.s = await api.update();
    upd.err = '';
    upd.restarting = false;
  } catch (e) {
    if (running() || upd.startedHere) upd.restarting = true;
    else upd.err = e instanceof Error ? e.message : String(e);
  }
  if (upd.startedHere && upd.s?.run && upd.s.run.state !== 'running') done();
  schedule();
}

/** start once, from the app: the first look, then every 30 min */
export function watchUpdates() {
  if (started) return;
  started = true;
  void load();
}

async function act(what: typeof upd.busy, fn: () => Promise<UpdateStatus>): Promise<boolean> {
  upd.busy = what;
  upd.err = '';
  try {
    upd.s = await fn();
    return true;
  } catch (e) {
    upd.err = e instanceof Error ? e.message : String(e);
    return false;
  } finally {
    upd.busy = '';
    schedule();
  }
}

export const checkNow = () => act('check', api.checkUpdate);
export const saveSettings = (s: UpdateSettings) => act('settings', () => api.setUpdateSettings(s));
export async function startUpdate(version: string) {
  if (await act('start', () => api.startUpdate(version))) {
    upd.startedHere = true;
    schedule();
  }
}

// The run this tab started is over. Served by the agent, the page itself was
// replaced too: load the new one. Through the controller only the router
// changed — nothing to reload.
function done() {
  upd.startedHere = false;
  const r = upd.s?.run;
  if (r?.state === 'done' && r.to === upd.s?.current && node.via === 'agent') {
    upd.reloading = true;
    setTimeout(() => location.reload(), 4000);
  }
}

/** a is a later version than b: X.Y.Z by number, a release after its pre-releases */
export function newer(a?: string, b?: string): boolean {
  if (!a || !b) return false;
  const split = (v: string) => {
    const [core, pre = ''] = v.replace(/^v/, '').split(/-(.*)/s);
    return { n: core.split('.').map((x) => Number(x) || 0), pre: pre ? pre.split('.') : [] };
  };
  const x = split(a);
  const y = split(b);
  for (let i = 0; i < 3; i++) if ((x.n[i] ?? 0) !== (y.n[i] ?? 0)) return (x.n[i] ?? 0) > (y.n[i] ?? 0);
  if (!x.pre.length || !y.pre.length) return !x.pre.length && !!y.pre.length;
  for (let i = 0; i < Math.min(x.pre.length, y.pre.length); i++) {
    const p = x.pre[i];
    const q = y.pre[i];
    if (p === q) continue;
    const pn = /^\d+$/.test(p);
    const qn = /^\d+$/.test(q);
    if (pn && qn) return Number(p) > Number(q);
    if (pn !== qn) return qn; // numbers before words
    return p > q;
  }
  return x.pre.length > y.pre.length;
}

/** a release's Markdown, as plain blocks: headings, list items, paragraphs */
export function notesBlocks(md: string): { kind: 'h' | 'li' | 'p'; text: string }[] {
  const out: { kind: 'h' | 'li' | 'p'; text: string }[] = [];
  const clean = (s: string) =>
    s
      .replace(/\*\*(.+?)\*\*/g, '$1')
      .replace(/`([^`]+)`/g, '$1')
      .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
      .trim();
  for (const raw of md.split('\n')) {
    const l = raw.trimEnd();
    if (!l.trim()) continue;
    const h = l.match(/^#{1,6}\s+(.*)/);
    const li = l.match(/^\s*[-*]\s+(.*)/);
    if (h) out.push({ kind: 'h', text: clean(h[1]) });
    else if (li) out.push({ kind: 'li', text: clean(li[1]) });
    else if (out.length && out[out.length - 1].kind !== 'h' && /^\s+/.test(raw)) out[out.length - 1].text += ' ' + clean(l);
    else out.push({ kind: 'p', text: clean(l) });
  }
  return out;
}
