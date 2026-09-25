// Small shared helpers for the views — formatting and labels only.
import type { EngineState, ListMode, PlaneStatus, Status } from './api';

export const MODES: { mode: ListMode; title: string; engine: 'nfqws2' | 'usque' | 'xray'; what: string }[] = [
  { mode: 'desync', title: 'DPI', engine: 'nfqws2', what: 'nfqws2 ломает DPI провайдера, трафик идёт напрямую' },
  { mode: 'warp', title: 'WARP', engine: 'usque', what: 'туннель Cloudflare WARP (usque); его рукопожатие тоже проходит через nfqws' },
  { mode: 'vless', title: 'VLESS', engine: 'xray', what: 'туннель на ваш VLESS-сервер (xray)' },
];

export const modeTitle = (m: string) => MODES.find((x) => x.mode === m)?.title ?? m;

/** The plane's status from /status, or null when PLANE is off. */
export function planeOf(s: Status | null | undefined): PlaneStatus | null {
  const p = s?.plane as (PlaneStatus & { backend?: string }) | null | undefined;
  if (!p || !p.backend || p.backend === 'off' || !('apply' in p)) return null;
  return p;
}

export function healthDot(e: Pick<EngineState, 'health'> | undefined): string {
  if (!e) return '';
  return e.health === 'ok' ? 'ok' : e.health === 'degraded' ? 'deg' : e.health === 'down' ? 'warn' : '';
}

export const HEALTH_LABEL: Record<string, string> = {
  ok: 'в порядке',
  degraded: 'частично',
  down: 'не работает',
  unknown: 'неизвестно',
};

export function fmtDur(s: number): string {
  s = Math.max(0, Math.floor(s || 0));
  const h = Math.floor(s / 3600),
    m = Math.floor((s % 3600) / 60);
  if (h >= 48) return `${Math.floor(h / 24)}д ${h % 24}ч`;
  if (h) return `${h}ч ${m}м`;
  if (m) return `${m}м ${s % 60}с`;
  return `${s}с`;
}

export function ago(unix?: number): string {
  if (!unix) return '—';
  const s = Math.max(0, Math.floor(Date.now() / 1000 - unix));
  if (s < 60) return `${s} с назад`;
  if (s < 3600) return `${Math.floor(s / 60)} мин назад`;
  if (s < 86400) return `${Math.floor(s / 3600)} ч назад`;
  return `${Math.floor(s / 86400)} д назад`;
}

/** Domains from pasted text: one per line, commas/spaces also split; # comments dropped. */
export function splitDomains(text: string): string[] {
  const out: string[] = [];
  for (const raw of text.split(/\n/)) {
    const line = raw.replace(/#.*$/, '');
    for (const d of line.split(/[\s,;]+/)) {
      const v = d
        .trim()
        .toLowerCase()
        .replace(/^https?:\/\//, '')
        .replace(/\/.*$/, '')
        .replace(/^\*\./, '');
      if (v && !out.includes(v)) out.push(v);
    }
  }
  return out;
}

export const OP_LABEL: Record<string, string> = {
  'create-group': 'создать группу',
  'add-domains': 'добавить домены',
  'del-domains': 'убрать домены',
  'add-route': 'добавить маршрут',
  'del-route': 'убрать маршрут',
  'delete-group': 'удалить группу',
};
