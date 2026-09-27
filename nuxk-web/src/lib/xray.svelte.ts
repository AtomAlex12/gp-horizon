// What the router's xray points at (GET /engines/xray/upstream), shared by the
// server card and the server list on the xray page.
import { api, type Upstream } from './api';

export const xray = $state<{ up: Upstream | null; err: string; busy: boolean }>({ up: null, err: '', busy: false });

export async function loadUpstream() {
  try {
    xray.up = await api.upstream('xray');
    xray.err = '';
  } catch (e) {
    xray.err = e instanceof Error ? e.message : String(e);
  }
}

/** run a change (pick, refresh, a new source); its answer is the new Upstream */
export async function change(fn: () => Promise<Upstream>): Promise<boolean> {
  xray.busy = true;
  xray.err = '';
  try {
    xray.up = await fn();
    return true;
  } catch (e) {
    xray.err = e instanceof Error ? e.message : String(e);
    return false;
  } finally {
    xray.busy = false;
  }
}

/** "раз в 12 ч" from seconds */
export const every = (s?: number) => (s ? `раз в ${Math.round(s / 3600)} ч` : 'раз в 12 ч');
