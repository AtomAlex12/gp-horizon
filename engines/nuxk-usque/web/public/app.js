(function () {
  'use strict';

  var API = 'api/index.php';
  var $ = function (id) { return document.getElementById(id); };

  /* --------------------------------------------------------------- i18n */
  var I18N = {
    ru: {
      'login.title': 'Вход', 'login.hint': 'Пользователь и пароль Entware',
      'login.user': 'Пользователь', 'login.password': 'Пароль',
      'login.error': 'Неверные данные', 'login.submit': 'Войти',
      'header.logout': 'Выход',
      'service.title': 'Сервис', 'service.pid': 'PID', 'service.uptime': 'Аптайм',
      'service.tunnel': 'Туннель', 'service.usque': 'usque', 'service.config': 'Конфиг',
      'action.start': 'Запуск', 'action.stop': 'Стоп', 'action.restart': 'Рестарт',
      'action.reregister': 'Ре-регистрация',
      'health.title': 'Здоровье', 'health.egress': 'Внешний IP', 'health.pop': 'PoP Cloudflare',
      'health.rtt': 'RTT', 'health.checked': 'Проверено', 'health.probe': 'Проверить сейчас',
      'iface.title': 'Интерфейс', 'iface.name': 'Имя', 'iface.ip': 'IP', 'iface.mtu': 'MTU',
      'iface.link': 'Link', 'iface.ndm': 'NDM', 'iface.endpoint': 'Endpoint',
      'traffic.title': 'Трафик', 'traffic.rate': 'скорость', 'traffic.total': 'Всего ↓ / ↑',
      'traffic.errors': 'Ошибки / дропы',
      'routes.title': 'Маршруты в туннель',
      'config.title': 'Конфиг', 'config.save': 'Сохранить и перезапустить',
      'log.title': 'Лог', 'log.follow': 'следить',
      'confirm.cancel': 'Отмена', 'confirm.ok': 'Подтвердить',
      'state.running': 'работает', 'state.stopped': 'остановлен',
      'tunnel.connected': 'подключён', 'tunnel.disconnected': 'отключён',
      'tunnel.unknown': 'неизвестно', 'tunnel.stopped': 'остановлен',
      'confirm.stop': 'Остановить сервис usque?',
      'confirm.restart': 'Перезапустить сервис usque?',
      'confirm.reregister': 'Сбросить device key и зарегистрироваться заново? Туннель прервётся.',
      'confirm.config': 'Сохранить конфиг и перезапустить сервис?',
      'toast.saved': 'Сохранено', 'toast.done': 'Готово', 'toast.failed': 'Ошибка'
    },
    en: {
      'login.title': 'Sign in', 'login.hint': 'Entware username and password',
      'login.user': 'User', 'login.password': 'Password',
      'login.error': 'Invalid credentials', 'login.submit': 'Sign in',
      'header.logout': 'Log out',
      'service.title': 'Service', 'service.pid': 'PID', 'service.uptime': 'Uptime',
      'service.tunnel': 'Tunnel', 'service.usque': 'usque', 'service.config': 'Config',
      'action.start': 'Start', 'action.stop': 'Stop', 'action.restart': 'Restart',
      'action.reregister': 'Re-register',
      'health.title': 'Health', 'health.egress': 'Egress IP', 'health.pop': 'Cloudflare PoP',
      'health.rtt': 'RTT', 'health.checked': 'Checked', 'health.probe': 'Check now',
      'iface.title': 'Interface', 'iface.name': 'Name', 'iface.ip': 'IP', 'iface.mtu': 'MTU',
      'iface.link': 'Link', 'iface.ndm': 'NDM', 'iface.endpoint': 'Endpoint',
      'traffic.title': 'Traffic', 'traffic.rate': 'rate', 'traffic.total': 'Total ↓ / ↑',
      'traffic.errors': 'Errors / drops',
      'routes.title': 'Routes into tunnel',
      'config.title': 'Config', 'config.save': 'Save & restart',
      'log.title': 'Log', 'log.follow': 'follow',
      'confirm.cancel': 'Cancel', 'confirm.ok': 'Confirm',
      'state.running': 'running', 'state.stopped': 'stopped',
      'tunnel.connected': 'connected', 'tunnel.disconnected': 'disconnected',
      'tunnel.unknown': 'unknown', 'tunnel.stopped': 'stopped',
      'confirm.stop': 'Stop the usque service?',
      'confirm.restart': 'Restart the usque service?',
      'confirm.reregister': 'Reset the device key and re-register? The tunnel will drop.',
      'confirm.config': 'Save config and restart the service?',
      'toast.saved': 'Saved', 'toast.done': 'Done', 'toast.failed': 'Failed'
    }
  };
  var lang = localStorage.getItem('usque.lang') || (navigator.language || 'en').slice(0, 2);
  if (!I18N[lang]) lang = 'en';
  function t(k) { return (I18N[lang] && I18N[lang][k]) || (I18N.en[k]) || k; }
  function applyI18n() {
    document.documentElement.lang = lang;
    document.querySelectorAll('[data-i18n]').forEach(function (el) {
      el.textContent = t(el.getAttribute('data-i18n'));
    });
    $('btn-lang').textContent = lang === 'ru' ? 'EN' : 'RU';
  }

  /* --------------------------------------------------------------- api */
  function api(cmd, params) {
    var body = new URLSearchParams(Object.assign({ cmd: cmd }, params || {}));
    return fetch(API, { method: 'POST', body: body, credentials: 'same-origin' })
      .then(function (r) {
        if (r.status === 401) { showLogin(); throw new Error('unauthorized'); }
        return r.json();
      });
  }

  /* --------------------------------------------------------------- format */
  function fmtBytes(n) {
    n = Number(n) || 0;
    var u = ['B', 'KB', 'MB', 'GB', 'TB'], i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i === 0 ? n : n.toFixed(n < 10 ? 2 : 1)) + ' ' + u[i];
  }
  function fmtRate(bps) { return fmtBytes(bps) + '/s'; }
  function fmtDur(s) {
    s = Number(s) || 0;
    var d = Math.floor(s / 86400); s %= 86400;
    var h = Math.floor(s / 3600); s %= 3600;
    var m = Math.floor(s / 60);
    if (d) return d + 'd ' + h + 'h';
    if (h) return h + 'h ' + m + 'm';
    if (m) return m + 'm';
    return Math.floor(s) + 's';
  }
  function ago(ts) {
    ts = Number(ts) || 0;
    if (!ts) return '—';
    return fmtDur(Math.max(0, Date.now() / 1000 - ts));
  }

  /* --------------------------------------------------------------- sparkline */
  var spark = { rx: [], tx: [], max: 120 };
  function pushSpark(rxRate, txRate) {
    spark.rx.push(rxRate); spark.tx.push(txRate);
    if (spark.rx.length > spark.max) { spark.rx.shift(); spark.tx.shift(); }
    drawSpark();
  }
  function drawSpark() {
    var c = $('spark'), ctx = c.getContext('2d');
    var w = c.width = c.clientWidth * (window.devicePixelRatio || 1);
    var h = c.height = 64 * (window.devicePixelRatio || 1);
    ctx.clearRect(0, 0, w, h);
    var all = spark.rx.concat(spark.tx);
    var peak = Math.max.apply(null, all.concat([1]));
    var css = getComputedStyle(document.documentElement);
    function line(arr, color) {
      ctx.beginPath();
      for (var i = 0; i < arr.length; i++) {
        var x = (i / (spark.max - 1)) * w;
        var y = h - (arr[i] / peak) * (h - 4) - 2;
        i ? ctx.lineTo(x, y) : ctx.moveTo(x, y);
      }
      ctx.strokeStyle = color; ctx.lineWidth = 1.5 * (window.devicePixelRatio || 1);
      ctx.stroke();
    }
    line(spark.rx, css.getPropertyValue('--accent').trim() || '#f6821f');
    line(spark.tx, css.getPropertyValue('--muted').trim() || '#888');
  }

  /* --------------------------------------------------------------- render */
  var prev = null, prevTs = 0;

  function renderStatus(d) {
    if (d.anonym) $('btn-logout').classList.add('hidden');
    else $('btn-logout').classList.remove('hidden');

    var v = d.version || {};
    $('hdr-ver').textContent = (d.web_version ? 'web ' + d.web_version : '') +
      (v.usque ? '  ·  usque ' + v.usque : '');

    var svc = d.service || {}, tun = d.tunnel || {};
    var running = svc.running === 1 || svc.running === '1';

    $('svc-state').textContent = running ? t('state.running') : t('state.stopped');
    $('svc-pid').textContent = svc.pid || '—';
    $('svc-uptime').textContent = running ? fmtDur(svc.uptime_s) : '—';
    var ts = tun.state || 'unknown';
    $('svc-tunnel').textContent = t('tunnel.' + ts) + (tun.since && ts === 'connected' ? ' · ' + ago(tun.since) : '');
    $('svc-usque').textContent = v.usque || '—';
    $('svc-config').textContent = v.config != null ? ('v' + v.config) : '—';

    var dot = $('hdr-dot'); dot.className = 'dot ' +
      (!running ? 'idle' : ts === 'connected' ? 'ok' : ts === 'disconnected' ? 'warn' : 'idle');

    var i = d.iface || {};
    $('if-name').textContent = i.name ? i.name + (i.label ? ' (' + i.label + ')' : '') : '—';
    $('if-ip').textContent = i.ip || '—';
    $('if-mtu').textContent = i.mtu || '—';
    $('if-link').textContent = i.link || '—';
    $('if-ndm').textContent = i.ndm_state || '—';
    $('if-endpoint').textContent = (d.tunnel && d.tunnel.endpoint) || '—';

    var tf = d.traffic || {};
    var now = Date.now() / 1000;
    if (prev && prevTs && now > prevTs && running) {
      var dt = now - prevTs;
      var rxr = Math.max(0, (tf.rx_bytes - prev.rx_bytes) / dt);
      var txr = Math.max(0, (tf.tx_bytes - prev.tx_bytes) / dt);
      $('tf-rx-rate').textContent = fmtRate(rxr);
      $('tf-tx-rate').textContent = fmtRate(txr);
      pushSpark(rxr, txr);
    }
    prev = tf; prevTs = now;
    $('tf-total').textContent = fmtBytes(tf.rx_bytes) + ' / ' + fmtBytes(tf.tx_bytes);
    $('tf-err').textContent = ((tf.rx_errors || 0) + (tf.tx_errors || 0)) + ' / ' +
      ((tf.rx_dropped || 0) + (tf.tx_dropped || 0));

    var r = d.routes || { count: 0, list: [] };
    $('rt-count').textContent = r.count || 0;
    var box = $('rt-list');
    box.innerHTML = '';
    (r.list || []).forEach(function (x) {
      var s = document.createElement('span'); s.textContent = x; box.appendChild(s);
    });

    setEnabled('btn-start', !running);
    setEnabled('btn-stop', running);
    setEnabled('btn-restart', running);
  }

  function renderProbe(d) {
    var ok = d.ok === 1 || d.ok === '1';
    $('hl-warp').textContent = ok ? ('warp=' + (d.warp || '?')) : (d.reason || 'offline');
    $('hl-ip').textContent = d.egress_ip || '—';
    $('hl-pop').textContent = d.colo ? (d.colo + (d.loc ? ' / ' + d.loc : '')) : '—';
    $('hl-rtt').textContent = d.rtt_ms ? (d.rtt_ms + ' ms') : '—';
    $('hl-ts').textContent = d.ts ? ago(d.ts) : '—';
  }

  function setEnabled(id, on) { $(id).disabled = !on; }

  /* --------------------------------------------------------------- polls */
  function pollStatus() {
    api('status').then(renderStatus).catch(function () {}).finally(function () {
      clearTimeout(pollStatus._t);
      pollStatus._t = setTimeout(pollStatus, 2000);
    });
  }
  function pollProbe() {
    api('probe').then(renderProbe).catch(function () {}).finally(function () {
      clearTimeout(pollProbe._t);
      pollProbe._t = setTimeout(pollProbe, 15000);
    });
  }
  function pollLog() {
    clearTimeout(pollLog._t);
    if ($('log-follow').checked) {
      api('log', { lines: 400 }).then(function (d) {
        $('log-view').textContent = d.content || '—';
      }).catch(function () {});
    }
    pollLog._t = setTimeout(pollLog, 4000);
  }

  /* --------------------------------------------------------------- actions */
  function confirmDialog(key) {
    return new Promise(function (res) {
      var dlg = $('confirm');
      $('confirm-text').textContent = t(key);
      dlg.returnValue = '';
      dlg.showModal();
      dlg.addEventListener('close', function h() {
        dlg.removeEventListener('close', h);
        res(dlg.returnValue === 'ok');
      });
    });
  }
  function toast(msg, err) {
    var el = $('toast');
    el.textContent = msg;
    el.className = 'toast' + (err ? ' err' : '');
    clearTimeout(toast._t);
    toast._t = setTimeout(function () { el.classList.add('hidden'); }, 3500);
  }
  function runAction(cmd, confirmKey) {
    var go = confirmKey ? confirmDialog(confirmKey) : Promise.resolve(true);
    go.then(function (ok) {
      if (!ok) return;
      ['btn-start', 'btn-stop', 'btn-restart', 'btn-reregister'].forEach(function (b) { $(b).disabled = true; });
      api(cmd).then(function (d) {
        toast(d.status === 0 ? t('toast.done') : (t('toast.failed') + ': ' + ((d.output || []).slice(-1)[0] || d.error || '')), d.status !== 0);
        pollStatus();
      }).catch(function () { toast(t('toast.failed'), true); });
    });
  }

  function loadConfig() {
    api('config_get').then(function (d) {
      $('cfg-sni').value = d.SNI || '';
      $('cfg-ip').value = d.IFACE_IP || '';
      $('cfg-http2').checked = String(d.HTTP2_ENABLE) === '1';
    }).catch(function () {});
  }

  /* --------------------------------------------------------------- login */
  function showLogin() { $('login').classList.remove('hidden'); }
  function hideLogin() { $('login').classList.add('hidden'); }

  /* --------------------------------------------------------------- boot */
  function bind() {
    $('btn-lang').onclick = function () {
      lang = lang === 'ru' ? 'en' : 'ru';
      localStorage.setItem('usque.lang', lang);
      applyI18n();
    };
    $('btn-logout').onclick = function () { api('logout').then(showLogin); };
    $('btn-start').onclick = function () { runAction('start'); };
    $('btn-stop').onclick = function () { runAction('stop', 'confirm.stop'); };
    $('btn-restart').onclick = function () { runAction('restart', 'confirm.restart'); };
    $('btn-reregister').onclick = function () { runAction('reregister', 'confirm.reregister'); };
    $('btn-probe').onclick = function () { api('probe').then(renderProbe); };
    $('log-follow').onchange = pollLog;

    $('config-form').onsubmit = function (e) {
      e.preventDefault();
      confirmDialog('confirm.config').then(function (ok) {
        if (!ok) return;
        api('config_set', {
          sni: $('cfg-sni').value.trim(),
          iface_ip: $('cfg-ip').value.trim(),
          http2: $('cfg-http2').checked ? '1' : '0'
        }).then(function (d) {
          toast(d.status === 0 ? t('toast.saved') : (t('toast.failed') + ': ' + ((d.output || [])[0] || '')), d.status !== 0);
          pollStatus();
        });
      });
    };

    $('login-form').onsubmit = function (e) {
      e.preventDefault();
      api('login', { user: $('login-user').value, password: $('login-password').value })
        .then(function (d) {
          if (d.status === 0) {
            hideLogin(); $('login-error').classList.add('hidden');
            started = false; start();
          } else {
            $('login-error').classList.remove('hidden');
          }
        }).catch(function () {});
    };

    window.addEventListener('resize', drawSpark);
  }

  var started = false;
  function start() {
    if (started) return;
    started = true;
    pollStatus(); pollProbe(); pollLog(); loadConfig();
  }

  applyI18n();
  bind();
  start();
})();
