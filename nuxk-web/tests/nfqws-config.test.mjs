import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const source = readFileSync(new URL('../src/lib/nfqws-config.ts', import.meta.url), 'utf8');
const js = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2020, module: ts.ModuleKind.ESNext } }).outputText;
const { configBlocks, replaceBlock } = await import('data:text/javascript;base64,' + Buffer.from(js).toString('base64'));

test('reads multiline strategies and quoted or unquoted scalar settings', () => {
  const text = '# Settings\nISP_INTERFACE="eth3"\nNFQWS_ARGS="--filter-tcp=443 \\\n--payload=tls_client_hello"\nMODE_FILTER=auto # keep this\nTCP_PORTS=80,443\nPOLICY_NAME=\'nfqws\'\n';
  assert.deepEqual(configBlocks(text).map(({ key, value }) => [key, value]), [
    ['ISP_INTERFACE', 'eth3'], ['NFQWS_ARGS', '--filter-tcp=443\n--payload=tls_client_hello'],
    ['MODE_FILTER', 'auto'], ['TCP_PORTS', '80,443'], ['POLICY_NAME', 'nfqws'],
  ]);
  assert.equal(replaceBlock(text, 'MODE_FILTER', 'list'), text.replace('MODE_FILTER=auto', 'MODE_FILTER="list"'));
});

test('repeated edits do not accumulate spaces and preserve unrelated content', () => {
  let text = '# prefix\nNFQWS_ARGS="a \\\nb"\n# suffix\nOTHER="unchanged"\n';
  const original = text;
  for (let i = 0; i < 10; i++) text = replaceBlock(text, 'NFQWS_ARGS', configBlocks(text)[0].value);
  assert.equal(text, original);
});

test('adds missing fields without replacing unknown content', () => {
  assert.equal(replaceBlock('# preserved', 'UDP_PORTS', '443'), '# preserved\nUDP_PORTS="443"\n');
});

test('does not reinterpret unsupported shell assignments or duplicates', () => {
  assert.throws(() => replaceBlock('TCP_PORTS=$(get_ports)\n', 'TCP_PORTS', '443'));
  assert.throws(() => replaceBlock('TCP_PORTS="80"\nTCP_PORTS="443"\n', 'TCP_PORTS', '80,443'));
  assert.throws(() => replaceBlock('', 'NOT-VALID', 'x'));
});

test('preserves single quotes and plain multiline values', () => {
  const text = "CUSTOM='first\nsecond' # note\n";
  assert.equal(replaceBlock(text, 'CUSTOM', 'a\nb'), "CUSTOM='a\nb' # note\n");
});
