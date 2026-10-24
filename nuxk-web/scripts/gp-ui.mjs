// Puts GP Plane's shared UI package (@gp-plane/gp-ui) into vendor/: the exact
// archive GP Plane handed over (GP-coordination, GP-UI-SHARING-001), checked
// against its SHA256 — fetched, not committed. Run it before `npm ci`
// (`npm run vendor`). Node rather than sh: the same on Windows, Alpine and
// macOS. A new version: change the three constants together with the "file:"
// entry in package.json, then `npm install` to refresh the lock.
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const NAME = 'gp-plane-gp-ui-0.1.0-s2a.1.tgz';
const SRC = `https://raw.githubusercontent.com/balbomush/GP-plane-web/36525eddcba1114821cb0d3093651e58befa88f9/frontend/vendor/${NAME}`;
const SUM = 'a1ed233f5ae2fd9830c2158737ad080f1deacfb84090d0efe88ce365b3289498';

const dir = join(dirname(fileURLToPath(import.meta.url)), '..', 'vendor');
const file = join(dir, NAME);
const sha256 = (b) => createHash('sha256').update(b).digest('hex');

if (existsSync(file) && sha256(readFileSync(file)) === SUM) process.exit(0);

let body;
for (let i = 1; !body; i++) {
  try {
    const r = await fetch(SRC, { signal: AbortSignal.timeout(60_000) });
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
    body = Buffer.from(await r.arrayBuffer());
  } catch (e) {
    if (i === 3) {
      console.error(`gp-ui: не скачался (${e instanceof Error ? e.message : e}): ${SRC}`);
      process.exit(1);
    }
  }
}
if (sha256(body) !== SUM) {
  console.error('gp-ui: SHA256 архива не совпал — это не тот пакет, который передал GP Plane');
  process.exit(1);
}
mkdirSync(dir, { recursive: true });
writeFileSync(file + '.part', body);
renameSync(file + '.part', file);
console.log(`gp-ui: vendor/${NAME}`);
