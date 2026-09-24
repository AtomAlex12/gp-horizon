import { api, HttpError, type Status } from './api';

// Single reactive status store, polled from nuxk-core /api/v1/status
// (which the daemon's reconcile loop keeps fresh).
export const status = $state<{
  data: Status | null;
  error: string | null;
  loading: boolean;
  needToken: boolean; // nuxk-core answered 401 — show the token form
}>({ data: null, error: null, loading: true, needToken: false });

let timer: ReturnType<typeof setTimeout> | undefined;

async function poll() {
  try {
    status.data = await api.status();
    status.error = null;
    status.needToken = false;
  } catch (e) {
    status.needToken = e instanceof HttpError && e.status === 401;
    status.error = e instanceof Error ? e.message : String(e);
  } finally {
    status.loading = false;
    timer = setTimeout(poll, 2000);
  }
}

export function startPolling() {
  if (timer === undefined) void poll();
}

export function stopPolling() {
  clearTimeout(timer);
  timer = undefined;
}

/** Poll right now (after the token was entered). */
export function pollNow() {
  clearTimeout(timer);
  void poll();
}

/** Force an immediate refresh (after an action). */
export async function refresh() {
  try {
    status.data = await api.status();
  } catch {
    /* next poll will surface it */
  }
}
