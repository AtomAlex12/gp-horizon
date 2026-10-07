// A run to start again: «Повторить» in «Результаты» fills the form in
// «Прогоны» (taken once, then cleared).
import type { RunMode, RunSettingsPatch } from './gp';

export const runDraft = $state<{ v: { domains: string[]; mode?: RunMode; settings?: RunSettingsPatch } | null }>({ v: null });
