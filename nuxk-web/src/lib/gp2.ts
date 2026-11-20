import { req } from './api';
import type { components } from './gp2-schema';

export type GP = components['schemas'];
export const gp2URL = (path: string) => '/ctl/v1/gp2/' + path;
export const gp2 = <T>(path: string, method = 'GET', body?: unknown) => req<T>(method, gp2URL(path), body);

export async function gp2Pages<T>(path: string): Promise<T[]> {
  const items: T[] = [];
  const join = path.includes('?') ? '&' : '?';
  for (;;) {
    const page = await gp2<{ items: T[]; total: number }>(path + join + 'limit=200&offset=' + items.length);
    items.push(...page.items);
    if (!page.items.length || items.length >= page.total) return items;
  }
}

export async function watchSearch(signal: AbortSignal, update: (value: GP['CurrentSearch']) => void) {
  const response = await fetch(gp2URL('search/events'), { signal });
  if (!response.ok || !response.body) throw new Error('Не удалось подписаться на состояние поиска.');
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let pending = '';
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      pending += value;
      const frames = pending.split(/\r?\n\r?\n/);
      pending = frames.pop() ?? '';
      for (const frame of frames) {
        const lines = frame.split(/\r?\n/);
        if (!lines.some(line => line.trim() === 'event: search')) continue;
        const data = lines.filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n');
        if (data) update(JSON.parse(data));
      }
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}
