#!/usr/bin/env node
/*
 * Local mock backend for developing web/public/ (the real dashboard frontend)
 * without a router. Serves the static frontend and answers /api/index.php with
 * canned responses shaped like web/backend/index.php.
 *
 *   node web/dev/mock-server.js        # http://localhost:8419
 *
 * For the fully-interactive demo (client-side state machine, all buttons live,
 * scenarios, routing presets) open web/demo/index.html directly instead.
 */
const http = require('http');
const fs = require('fs');
const path = require('path');

const ROOT = path.join(__dirname, '..', 'public');
const PORT = process.env.PORT || 8419;
const MIME = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.yaml': 'application/yaml' };

let rx = 1e7, tx = 2e6;
const started = Date.now() - 3600 * 1000;

function status() {
  rx += Math.random() * 9e5;
  tx += Math.random() * 2e5;
  return {
    status: 0, anonym: false, web_version: '0.4.0', name: 'USQUE',
    version: { pkg: '0.4.0', usque: '4.2.0', config: 1 },
    service: { running: 1, pid: 1234, uptime_s: Math.floor((Date.now() - started) / 1000) },
    tunnel: { state: 'connected', since: Math.floor(started / 1000), endpoint: '162.159.198.2' },
    iface: { name: 'opkgtun0', ndm_name: 'OpkgTun0', label: 'usque', ip: '172.16.1.100', mtu: 1280, link: 'up', ndm_state: 'up' },
    traffic: { rx_bytes: Math.floor(rx), tx_bytes: Math.floor(tx), rx_packets: 40213, tx_packets: 22887,
               rx_errors: 0, tx_errors: 0, rx_dropped: 3, tx_dropped: 0 },
    config: { sni: 'ozon.ru', http2: 0, iface_ip: '' },
    routes: { count: 6, list: ['104.16.0.0/13', '172.64.0.0/13', '1.1.1.1/32', '8.8.8.8/32', '149.154.160.0/20', '31.13.24.0/21'] },
  };
}

const server = http.createServer((req, res) => {
  if (req.url.startsWith('/api/')) {
    let b = '';
    req.on('data', (c) => (b += c));
    req.on('end', () => {
      const cmd = new URLSearchParams(b).get('cmd');
      let out = { status: 0 };
      if (cmd === 'status') out = status();
      else if (cmd === 'probe') out = { status: 0, ok: 1, egress_ip: '104.28.51.9', warp: 'on', colo: 'DME', loc: 'RU', rtt_ms: '18.4', ts: Math.floor(Date.now() / 1000) };
      else if (cmd === 'log') out = { status: 0, content: Array.from({ length: 30 }, (_, i) => `2026-08-31T21:${String(10 + i).padStart(2, '0')}:00 INFO  tunnel up, colo=DME`).reverse().join('\n') };
      else if (cmd === 'config_get') out = { status: 0, SNI: 'ozon.ru', HTTP2_ENABLE: '0', IFACE_IP: '', IFACE: 'opkgtun0' };
      else if (cmd === 'config_set') out = { status: 0, output: ['Started USQUE service'] };
      else if (['start', 'stop', 'restart', 'reregister'].includes(cmd)) out = { status: 0, output: [`${cmd} ok`] };
      else if (cmd === 'login') out = { status: 0 };
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify(out));
    });
    return;
  }
  const rel = (req.url === '/' ? '/index.html' : req.url.split('?')[0]);
  const fp = path.join(ROOT, rel);
  if (!fp.startsWith(path.normalize(ROOT)) || !fs.existsSync(fp)) { res.writeHead(404); res.end('not found'); return; }
  res.writeHead(200, { 'Content-Type': MIME[path.extname(fp)] || 'text/plain; charset=utf-8' });
  res.end(fs.readFileSync(fp));
});

server.listen(PORT, () => console.log(`mock backend + frontend on http://localhost:${PORT}`));
