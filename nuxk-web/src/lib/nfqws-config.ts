// Read literal shell assignments without evaluating the router's configuration.
export type ConfigBlock = { key: string; value: string; start: number; end: number; quote: string };

export function configBlocks(text: string): ConfigBlock[] {
  const assignment = /^([A-Z_][A-Z0-9_]*)=(?:"((?:[^"\\]|\\[\s\S])*)"|'([^']*)'|([\w,.:/@+-]*))(?=[ \t]*(?:#|\r?$))/gm;
  return [...text.matchAll(assignment)].map(match => ({
    key: match[1],
    value: (match[2] ?? match[3] ?? match[4]).replace(/[ \t]*\\\r?\n/g, '\n'),
    start: match.index!, end: match.index! + match[0].length,
    quote: match[3] === undefined ? '"' : "'",
  }));
}

export function replaceBlock(text: string, key: string, value: string): string {
  if (!/^[A-Z_][A-Z0-9_]*$/.test(key)) throw new Error('Некорректное имя переменной.');
  const found = configBlocks(text).filter(block => block.key === key);
  const occurrences = [...text.matchAll(new RegExp('^[ \\t]*(?:export[ \\t]+)?' + key + '=', 'gm'))];
  if (occurrences.length > 1 || (occurrences.length && !found.length)) {
    throw new Error('Этот параметр содержит сложное выражение или повторяется. Измените его в полном файле.');
  }
  if (/["'\\]/.test(value)) throw new Error('Для кавычек и обратных косых черт откройте полный файл.');
  const block = found[0];
  const quote = block?.quote ?? '"';
  const joined = quote === "'" ? value : value.split('\n').join(' \\\n');
  const replacement = key + '=' + quote + joined + quote;
  if (!block) return text + (text && !text.endsWith('\n') ? '\n' : '') + replacement + '\n';
  return text.slice(0, block.start) + replacement + text.slice(block.end);
}
