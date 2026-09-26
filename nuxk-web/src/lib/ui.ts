// Small shared helpers for the views — formatting and labels only.
import type { EngineState, ListMode, NodeInfo, PlaneStatus, Status } from './api';

export const ENGINE_LABEL: Record<string, string> = {
  nfqws2: 'nfqws2 (DPI)',
  usque: 'usque (WARP)',
  xray: 'xray (VLESS)',
};

export const MODES: { mode: ListMode; title: string; engine: 'nfqws2' | 'usque' | 'xray'; what: string }[] = [
  { mode: 'desync', title: 'DPI', engine: 'nfqws2', what: 'nfqws2 ломает DPI провайдера, трафик идёт напрямую' },
  { mode: 'warp', title: 'WARP', engine: 'usque', what: 'туннель Cloudflare WARP (usque); его рукопожатие тоже проходит через nfqws' },
  { mode: 'vless', title: 'VLESS', engine: 'xray', what: 'туннель на ваш VLESS-сервер (xray)' },
];

export const modeTitle = (m: string) => MODES.find((x) => x.mode === m)?.title ?? m;

/** The plane's status from /status, or null when PLANE is off. */
export function planeOf(s: Status | null | undefined): PlaneStatus | null {
  const p = s?.plane as PlaneStatus | { backend: string } | null | undefined;
  if (!p || !('apply' in p)) return null;
  return p;
}

/** What this box is, in words — a stand must never pass for the router. */
export function roleLabel(i: NodeInfo | null): string {
  if (!i) return 'nuxk-core';
  if (i.role === 'router') return ['Роутер', i.model || 'Keenetic'].join(' ');
  if (i.role === 'stand') return 'Стенд Pi (Docker)';
  return `Хост ${i.hostname}`;
}

export function fmtBps(v: number): string {
  if (v >= 1e9) return `${(v / 1e9).toFixed(v >= 1e10 ? 0 : 1)} Гбит/с`;
  if (v >= 1e6) return `${(v / 1e6).toFixed(v >= 1e7 ? 0 : 1)} Мбит/с`;
  if (v >= 1e3) return `${(v / 1e3).toFixed(v >= 1e4 ? 0 : 1)} кбит/с`;
  return `${Math.round(v)} бит/с`;
}

/** Axis ticks: the same scale without the unit clutter. */
export function fmtBpsShort(v: number): string {
  if (v >= 1e9) return `${+(v / 1e9).toFixed(1)}G`;
  if (v >= 1e6) return `${+(v / 1e6).toFixed(1)}M`;
  if (v >= 1e3) return `${+(v / 1e3).toFixed(1)}k`;
  return `${Math.round(v)}`;
}

export function fmtNum(v: number): string {
  return v >= 100 ? String(Math.round(v)) : v >= 10 ? v.toFixed(0) : v >= 1 ? v.toFixed(1) : v === 0 ? '0' : v.toFixed(2);
}

export const last = (a: number[] | undefined) => (a && a.length ? a[a.length - 1] : 0);

export function healthDot(e: Pick<EngineState, 'health'> | undefined): string {
  if (!e) return '';
  return e.health === 'ok' ? 'ok' : e.health === 'degraded' ? 'deg' : e.health === 'down' ? 'warn' : '';
}

export function healthChip(e: Pick<EngineState, 'health'> | undefined): string {
  if (!e) return '';
  return e.health === 'ok' ? 'ok' : e.health === 'down' ? 'warn' : 'deg';
}

// as in the approved design: what the engine is doing, not a verdict — a
// running engine whose probe fails is "частично" (the agent reports degraded)
export const HEALTH_LABEL: Record<string, string> = {
  ok: 'работает',
  degraded: 'частично',
  down: 'остановлен',
  unknown: '—',
};

/** The probe line under an engine: "проба ok · 9 мс" / "проба: timeout_or_reset". */
export function probeText(e: Pick<EngineState, 'probe'>): string {
  const p = e.probe;
  if (!p) return 'проба не запускалась';
  if (p.ok) return `проба ok${p.rtt_ms ? ` · ${Math.round(p.rtt_ms)} мс` : ''}`;
  return `проба не прошла: ${p.reason ?? 'нет ответа'}`;
}

export function fmtDur(s: number): string {
  s = Math.max(0, Math.floor(s || 0));
  const h = Math.floor(s / 3600),
    m = Math.floor((s % 3600) / 60);
  if (h >= 48) return `${Math.floor(h / 24)}д ${h % 24}ч`;
  if (h) return `${h}ч ${m}м`;
  if (m) return `${m}м ${s % 60}с`;
  return `${s}с`;
}

/** "3 записи похожи": n with the Russian plural form for 1 / 2–4 / 5+. */
export function plural(n: number, one: string, few: string, many: string): string {
  const d = n % 10,
    h = n % 100;
  const w = d === 1 && h !== 11 ? one : d >= 2 && d <= 4 && (h < 12 || h > 14) ? few : many;
  return `${n} ${w}`;
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
