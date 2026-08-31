<?php

/**
 * usque-keenetic-web — thin API in front of /opt/etc/init.d/S51usque.
 *
 * All service state comes from `S51usque info` / `S51usque probe` (flat
 * "key value" lines). This layer only: authenticates, parses `cmd`,
 * shells out, and returns JSON.
 */

ini_set('memory_limit', '24M');
error_reporting(E_ALL & ~E_DEPRECATED & ~E_NOTICE);

const ROOT_DIR       = '/opt';
const INIT_SCRIPT    = ROOT_DIR . '/etc/init.d/S51usque';
const CONFIG_FILE    = ROOT_DIR . '/etc/usque/usque.conf';
const WEB_CONF_FILE  = ROOT_DIR . '/etc/usque_web.conf';
const LOG_FILE       = ROOT_DIR . '/var/log/usque.log';
const WEB_VERSION_FILE = ROOT_DIR . '/share/www/usque/version';

/* ------------------------------------------------------------------ helpers */

function web_version(): string
{
    return is_file(WEB_VERSION_FILE) ? trim(@file_get_contents(WEB_VERSION_FILE)) : '';
}

function init_exists(): bool
{
    return is_file(INIT_SCRIPT);
}

/** Run `S51usque <args>`; returns [exit_code, output_lines[]]. */
function init_run(string $args): array
{
    $output = [];
    $retval = 1;
    if (init_exists()) {
        exec(escapeshellarg(INIT_SCRIPT) . ' ' . $args . ' 2>&1', $output, $retval);
    }
    return [$retval, $output];
}

/** Parse flat "key value" lines into a nested array (dot keys → nesting). */
function parse_kv(array $lines): array
{
    $out = ['routes' => ['count' => 0, 'list' => []]];
    foreach ($lines as $line) {
        $line = rtrim($line, "\r\n");
        if ($line === '') {
            continue;
        }
        $sp = strpos($line, ' ');
        if ($sp === false) {
            $key = $line;
            $val = '';
        } else {
            $key = substr($line, 0, $sp);
            $val = substr($line, $sp + 1);
        }

        if ($key === 'route') {
            $out['routes']['list'][] = $val;
            continue;
        }

        if (strpos($key, '.') !== false) {
            [$a, $b] = explode('.', $key, 2);
            $out[$a][$b] = kv_cast($val);
        } else {
            $out[$key] = kv_cast($val);
        }
    }
    return $out;
}

function kv_cast(string $v)
{
    if ($v === '') {
        return '';
    }
    if (preg_match('/^-?\d+$/', $v)) {
        return (int) $v;
    }
    return $v;
}

function service_info(): array
{
    [$rc, $lines] = init_run('info');
    $info = parse_kv($lines);
    $info['status'] = $rc === 0 ? 0 : 1;
    $info['web_version'] = web_version();
    return $info;
}

function service_probe(): array
{
    [$rc, $lines] = init_run('probe');
    $p = parse_kv($lines);
    $p['status'] = 0;
    return $p;
}

function log_tail(int $lines = 400): string
{
    if (!is_file(LOG_FILE)) {
        return '';
    }
    $all = @file(LOG_FILE, FILE_IGNORE_NEW_LINES);
    if ($all === false) {
        return '';
    }
    $all = array_slice($all, -$lines);
    return implode("\n", array_reverse($all));
}

/* ------------------------------------------------------------------ config */

const CONFIG_ALLOWED = ['SNI', 'HTTP2_ENABLE', 'IFACE_IP'];

function config_get(): array
{
    $res = ['SNI' => '', 'HTTP2_ENABLE' => '0', 'IFACE_IP' => '', 'IFACE' => ''];
    if (is_file(CONFIG_FILE)) {
        foreach (file(CONFIG_FILE, FILE_IGNORE_NEW_LINES) as $l) {
            if (preg_match('/^\s*(SNI|HTTP2_ENABLE|IFACE_IP|IFACE)\s*=\s*"?([^"]*)"?\s*$/', $l, $m)) {
                $res[$m[1]] = $m[2];
            }
        }
    }
    return $res;
}

function config_validate(string $key, string $val): ?string
{
    switch ($key) {
        case 'SNI':
            return preg_match('/^[a-zA-Z0-9._-]{0,253}$/', $val) ? null : 'invalid SNI';
        case 'HTTP2_ENABLE':
            return ($val === '0' || $val === '1') ? null : 'HTTP2_ENABLE must be 0 or 1';
        case 'IFACE_IP':
            if ($val === '') {
                return null;
            }
            return filter_var($val, FILTER_VALIDATE_IP, FILTER_FLAG_IPV4) !== false ? null : 'invalid IPv4';
    }
    return 'unknown key';
}

function config_set(array $pairs): array
{
    if (!is_file(CONFIG_FILE) || !is_writable(CONFIG_FILE)) {
        return ['status' => 1, 'output' => ['config file not writable']];
    }
    $content = file_get_contents(CONFIG_FILE);

    foreach ($pairs as $key => $val) {
        $key = strtoupper($key);
        if (!in_array($key, CONFIG_ALLOWED, true)) {
            return ['status' => 1, 'output' => ["key not allowed: $key"]];
        }
        $err = config_validate($key, $val);
        if ($err !== null) {
            return ['status' => 1, 'output' => [$err]];
        }

        if ($key === 'HTTP2_ENABLE') {
            $repl = "HTTP2_ENABLE=$val";
            $pat  = '/^\s*#?\s*HTTP2_ENABLE\s*=.*$/m';
        } else {
            $repl = $key . '="' . $val . '"';
            $pat  = '/^\s*#?\s*' . $key . '\s*=.*$/m';
        }

        if (preg_match($pat, $content)) {
            $content = preg_replace($pat, $repl, $content, 1);
        } else {
            $content .= "\n" . $repl . "\n";
        }
    }

    if (file_put_contents(CONFIG_FILE, $content) === false) {
        return ['status' => 1, 'output' => ['write failed']];
    }

    [$rc, $out] = init_run('restart');
    return ['status' => $rc, 'output' => $out];
}

/* ------------------------------------------------------------------ auth */

function auth_enabled(): bool
{
    if (!is_file(WEB_CONF_FILE)) {
        return true;
    }
    $cfg = parse_ini_file(WEB_CONF_FILE, true);
    return !isset($cfg['auth']['enabled'])
        || filter_var($cfg['auth']['enabled'], FILTER_VALIDATE_BOOLEAN);
}

function authenticate(string $username, string $password): bool
{
    if ($username === '') {
        return false;
    }
    $shadow = ROOT_DIR . '/etc/shadow';
    $passwd = ROOT_DIR . '/etc/passwd';
    $src = is_file($shadow) ? $shadow : $passwd;
    if (!is_readable($src)) {
        return false;
    }
    foreach (file($src, FILE_IGNORE_NEW_LINES) as $line) {
        $parts = explode(':', $line);
        if (($parts[0] ?? '') !== $username) {
            continue;
        }
        $hash = $parts[1] ?? '';
        if ($hash === '' || $hash === '!' || $hash === '*') {
            return $password === '';
        }
        return hash_equals(crypt($password, $hash), $hash);
    }
    return false;
}

/* ------------------------------------------------------------------ main */

function respond($data): void
{
    header('Content-Type: application/json; charset=utf-8');
    header('Cache-Control: no-store');
    echo json_encode($data);
    exit;
}

function main(): void
{
    if (($_SERVER['REQUEST_METHOD'] ?? '') !== 'POST') {
        http_response_code(405);
        respond(['status' => 1, 'error' => 'POST only']);
    }

    $cmd = $_POST['cmd'] ?? '';
    $authEnabled = auth_enabled();

    session_start();
    if ($cmd === 'login') {
        if (!$authEnabled) {
            $_SESSION['auth'] = true;
            respond(['status' => 0, 'anonym' => true]);
        }
        $ok = authenticate($_POST['user'] ?? '', $_POST['password'] ?? '');
        $_SESSION['auth'] = $ok;
        http_response_code($ok ? 200 : 401);
        respond(['status' => $ok ? 0 : 1]);
    }

    if ($authEnabled && empty($_SESSION['auth'])) {
        http_response_code(401);
        respond(['status' => 1, 'error' => 'unauthorized']);
    }

    switch ($cmd) {
        case 'status':
            respond(array_merge(service_info(), ['anonym' => !$authEnabled]));
            // no break

        case 'probe':
            respond(service_probe());
            // no break

        case 'log':
            $n = (int) ($_POST['lines'] ?? 400);
            $n = max(10, min($n, 2000));
            respond(['status' => 0, 'content' => log_tail($n)]);
            // no break

        case 'config_get':
            respond(array_merge(['status' => 0], config_get()));
            // no break

        case 'config_set':
            $pairs = [];
            foreach (['sni', 'http2', 'iface_ip'] as $f) {
                if (isset($_POST[$f])) {
                    $key = $f === 'http2' ? 'HTTP2_ENABLE' : strtoupper($f);
                    $pairs[$key] = trim($_POST[$f]);
                }
            }
            if (!$pairs) {
                respond(['status' => 1, 'output' => ['nothing to set']]);
            }
            respond(config_set($pairs));
            // no break

        case 'start':
        case 'stop':
        case 'restart':
        case 'reregister':
            [$rc, $out] = init_run($cmd);
            respond(['status' => $rc, 'output' => $out]);
            // no break

        case 'logout':
            $_SESSION['auth'] = false;
            respond(['status' => 0]);
            // no break

        default:
            http_response_code(400);
            respond(['status' => 1, 'error' => 'unknown cmd']);
    }
}

main();
