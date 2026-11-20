#!/bin/sh
# nuxk plugin recipe: GP Plane core (balbomush/GP-plane, formerly
# GP-access-control-plane) + the zapret2 it drives.
#
#   install.sh <gp-tag|commit> <dest>     (run by `nuxk-controller supervise`)
#
# Everything is fetched on this device from the projects' own releases — nuxk
# ships none of their code. Runs as root without any capability: it can
# download and write <dest>, nothing else.
#   GP:       the tag's source tarball — or, to test a build GP Plane hands
#             over without a tag, a full commit SHA's; Python deps pinned by
#             hash (GP's runtime lock, requirements/a5-transport.lock)
#   zapret2:  the release tarball, every binary checked against its sha256sum.txt
set -eu
V="$1"
D="$2"
Z="${ZAPRET_REF:?ZAPRET_REF is not set}"
cd "$D"
say() { printf '== %s\n' "$*"; }
get() { curl -fsSL --retry 3 --retry-delay 2 -o "$1" "$2"; }

say "GP $V"
ref="refs/tags/$V"
if [ ${#V} -eq 40 ] && ! printf %s "$V" | grep -q '[^0-9a-f]'; then
    ref="$V" # a commit: a test build, not a release
fi
if [ "$V" = v0.5.0-alpha.4 ] || [ "$V" = 8748e6c39248ddda5ee8ce7f32d3b5049f2432b8 ]; then
    get gp.tar.gz "https://github.com/balbomush/GP-plane/releases/download/v0.5.0-alpha.4/core-8748e6c39248ddda5ee8ce7f32d3b5049f2432b8.tar.gz"
    printf '%s\n' '78c089bd1071fab245ffef101113cf89454f877e72bb49cc2f95270724fd1f84  gp.tar.gz' | sha256sum -c -
    strip=0
else
    get gp.tar.gz "https://github.com/balbomush/GP-plane/archive/$ref.tar.gz"
    strip=1
fi
mkdir gp
tar -xzf gp.tar.gz -C gp --strip-components="$strip"
rm gp.tar.gz
[ -f gp/src/gp_control_plane/cli.py ] || { echo "в $V нет ядра GP (src/gp_control_plane)" >&2; exit 1; }
[ -f gp/scripts/gp-root-helper.sh ] || { echo "в $V нет scripts/gp-root-helper.sh" >&2; exit 1; }

say "Python-зависимости по хешам из lock-файла GP"
# the core's runtime lock by name: GP Plane's other locks (tests, builds) are not for this
lock=gp/requirements/a5-transport.lock
[ -f "$lock" ] || { echo "в $V нет requirements/a5-transport.lock — без хешей не ставлю" >&2; exit 1; }
python3 -m venv venv
venv/bin/pip install --no-cache-dir --disable-pip-version-check --require-hashes --only-binary=:all: -r "$lock"

say "zapret2 $Z"
get "zapret2-$Z.tar.gz" "https://github.com/bol-van/zapret2/releases/download/$Z/zapret2-$Z.tar.gz"
get sha256sum.txt "https://github.com/bol-van/zapret2/releases/download/$Z/sha256sum.txt"
tar -xzf "zapret2-$Z.tar.gz"
sha256sum --quiet -c sha256sum.txt
mv "zapret2-$Z" zapret2
rm -f "zapret2-$Z.tar.gz" sha256sum.txt
(cd zapret2 && ./install_bin.sh)
[ -x zapret2/nfq2/nfqws2 ] && [ -x zapret2/blockcheck2.sh ] || { echo "zapret2: нет nfqws2 или blockcheck2.sh для этой архитектуры" >&2; exit 1; }
zapret2/nfq2/nfqws2 --version 2>/dev/null | head -n 1 || true

install -m 0755 gp/scripts/gp-root-helper.sh gp-root-helper
if [ "$V" = v0.5.0-alpha.4 ] || [ "$V" = 8748e6c39248ddda5ee8ce7f32d3b5049f2432b8 ]; then
    # Stock executes its generated runner. The controller's /tmp is noexec.
    sed -i 's@mktemp -d /tmp/gp-api2-stock\.XXXXXX@mktemp -d "${TMPDIR:-/tmp}/gp-api2-stock.XXXXXX"@' gp-root-helper
    grep -Fq 'mktemp -d "${TMPDIR:-/tmp}/gp-api2-stock.XXXXXX"' gp-root-helper
fi

state=state
if [ "$V" = v0.5.0-alpha.4 ] || [ "$V" = 8748e6c39248ddda5ee8ce7f32d3b5049f2432b8 ]; then
    state=state-api2
fi

say "bin/run"
mkdir -p bin
cat >bin/run <<EOF
#!/bin/sh
# GP core, API only, on the plugin's loopback port. Started by the nuxk
# supervisor with PLUGIN_DIR / PLUGIN_DATA set and only the plugin's caps.
set -eu
D="\$PLUGIN_DIR"
state="\$PLUGIN_DATA/$state"
if [ "$state" = state-api2 ] && [ ! -d "\$state" ]; then
    staging=\$(mktemp -d "\$PLUGIN_DATA/.state-api2.XXXXXX")
    trap 'rm -rf "\$staging"' 0
    if [ -d "\$PLUGIN_DATA/state" ]; then
        cp -a "\$PLUGIN_DATA/state/." "\$staging/"
    fi
    mv "\$staging" "\$state"
    trap - 0
fi
mkdir -p "\$state"
export GP_INSTALL_DIR="\$D/gp" GP_STATE_DIR="\$state" GP_INSTALL_WEB=off GP_INSTALLED_REF="$V" \\
  ZAPRET_DIR="\$D/zapret2" GP_ROOT_HELPER="\$D/gp-root-helper" GP_BLOCKCHECK2D="\$D/zapret2/blockcheck2.d" \\
  PYTHONPATH="\$D/gp/src" PATH="\$D/zapret2:\$D/zapret2/nfq2:\$D/venv/bin:/usr/sbin:/usr/bin:/sbin:/bin"
exec "\$D/venv/bin/python" -c 'import sys; from gp_control_plane.cli import main; sys.exit(main(sys.argv[1:]))' core --host 127.0.0.1 --port 8081
EOF
chmod 0755 bin/run
say "готово: GP $V, zapret2 $Z"
