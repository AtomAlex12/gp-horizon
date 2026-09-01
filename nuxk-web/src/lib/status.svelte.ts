import { api, type Status } from './api';

// Single reactive status store, polled from nuxk-core /api/v1/status
// (which the daemon's reconcile loop keeps fresh).
export const status = $state<{
  data: Status | null;
  error: string | null;
  loading: boolean;
}>({ data: null, error: null, loading: true });

let timer: ReturnType<typeof setTimeout> | undefined;

async function poll() {
  try {
    status.data = await api.status();
    status.error = null;
  } catch (e) {
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

/** Force an immediate refresh (after an action). */
export async function refresh() {
  try {
    status.data = await api.status();
  } catch {
    /* next poll will surface it */
  }
}
