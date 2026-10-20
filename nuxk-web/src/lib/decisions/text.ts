// «Решения»: labels and number formats. Only what the agent measured is
// formatted — an absent value stays absent («—», «не измерена»).
import type { Suggestion, SuggestPath } from '../api';

type Verdict = NonNullable<Suggestion['verdict']>;

export const VERDICT: Record<Verdict, { label: string; cls: string }> = {
  ok: { label: 'открывается', cls: 'ok' },
  geoblock: { label: 'сайт блокирует Россию', cls: 'deg' },
  'sni-block': { label: 'блок по имени (DPI)', cls: 'warn' },
  'ip-block': { label: 'блок по адресу', cls: 'warn' },
  'dns-block': { label: 'подмена DNS', cls: 'warn' },
  freeze: { label: 'заморозка после 16 КБ', cls: 'warn' },
  slow: { label: 'медленно', cls: 'deg' },
  unknown: { label: 'непонятно', cls: '' },
};
export const verdictOf = (s: Suggestion) => VERDICT[s.verdict ?? 'unknown'] ?? VERDICT.unknown;

export const VIA_TITLE: Record<SuggestPath['via'], string> = {
  direct: 'напрямую',
  desync: 'DPI (nfqws2)',
  warp: 'WARP',
  vless: 'VLESS',
  dns: 'DNS',
};

export const SOURCE_TITLE: Record<Suggestion['source'], string> = {
  user: 'проверка вручную',
  scan: 'популярные сервисы',
  dns: 'найден по DNS',
};

const dec1 = (v: number) => v.toLocaleString('ru-RU', { minimumFractionDigits: 1, maximumFractionDigits: 1 });

/** kbit/s → "12,3 Мбит/с"; absent = the speed couldn't be measured. */
export const mbit = (kbps?: number) => (kbps ? `${dec1(kbps / 1000)} Мбит/с` : 'не измерена');

/** "640 мс"; absent = unknown. */
export const msText = (ms?: number) => (ms ? `${Math.round(ms).toLocaleString('ru-RU')} мс` : '—');

/** A moment ahead: "через 5 мин"; already past = it's due now. */
export function ahead(unix?: number): string {
  if (!unix) return '';
  const s = Math.floor(unix - Date.now() / 1000);
  if (s <= 30) return 'вот-вот';
  if (s < 3600) return `через ${Math.max(1, Math.round(s / 60))} мин`;
  if (s < 86400) return `через ${Math.round(s / 3600)} ч`;
  return `через ${Math.round(s / 86400)} д`;
}

/** Seconds with up to one decimal, the way people write them: "0,5", "5". */
export const secText = (ms: number) => (ms / 1000).toLocaleString('ru-RU', { maximumFractionDigits: 1 });
