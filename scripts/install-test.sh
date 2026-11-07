#!/bin/sh
# End-to-end tests for install.sh, run against a local fake release server.
# Usage: scripts/install-test.sh
# shellcheck shell=sh
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
readonly INSTALL_SH="$script_dir/../install.sh"

readonly RELEASE_V1="v0.1.0"
readonly RELEASE_V2="v0.2.0"

tmp=$(mktemp -d)
server_pid=
cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

pass() { printf 'ok - %s\n' "$1"; }
fail_test() { printf 'not ok - %s\n' "$1" >&2; exit 1; }

# make_uname_shim writes a uname replacement reporting the given kernel and
# machine, so tests can pin the platform install.sh detects.
make_uname_shim() {
  shim_dir=$1
  kernel=$2
  machine=$3
  mkdir -p "$shim_dir"
  cat >"$shim_dir/uname" <<EOF
#!/bin/sh
case "\$1" in
  -s) printf '%s\n' "$kernel" ;;
  -m) printf '%s\n' "$machine" ;;
  *) printf 'uname: unexpected invocation\n' >&2; exit 1 ;;
esac
EOF
  chmod +x "$shim_dir/uname"
}

# write_fake_binary creates an executable stand-in whose content names the
# version and platform it pretends to be.
write_fake_binary() {
  target_dir=$1
  version=$2
  goos=$3
  goarch=$4
  mkdir -p "$target_dir"
  printf '#!/bin/sh\nprintf "%%s\\n" "%s"\n' \
    "touchstone $version $goos/$goarch" >"$target_dir/touchstone"
  chmod 755 "$target_dir/touchstone"
}

# write_checksums regenerates the checksums file for VERSION, either correct
# or deliberately corrupt for the mismatch test.
write_checksums() {
  version=$1
  mode=$2
  checksums=$releases_dir/download/$version/checksums.txt
  : >"$checksums"
  for archive in "$releases_dir/download/$version"/*.tar.gz; do
    if [ "$mode" = correct ]; then
      digest=$(shasum -a 256 "$archive" | awk '{print $1}')
    else
      digest=0000000000000000000000000000000000000000000000000000000000000000
    fi
    printf '%s  %s\n' "$digest" "$(basename "$archive")" >>"$checksums"
  done
}

# run_installer runs install.sh against the fake server with the given uname
# shim and arguments, printing its output only when a test fails.
run_installer() {
  shim_dir=$1
  install_dir=$2
  shift 2
  if ! (PATH="$shim_dir:$PATH" TOUCHSTONE_RELEASE_URL="$release_url" \
    TOUCHSTONE_INSTALL_DIR="$install_dir" sh "$INSTALL_SH" "$@") >"$tmp/out.log" 2>&1; then
    return 1
  fi
  cat "$tmp/out.log"
}

shasum -a 256 /dev/null >/dev/null 2>&1 || fail_test "shasum is required to run these tests"
python3 -c '' 2>/dev/null || fail_test "python3 is required to run these tests"

# Build a fake release tree: two pinned releases plus the redirect target.
releases_dir=$tmp/releases
for version in "$RELEASE_V1" "$RELEASE_V2"; do
  for platform in "linux amd64" "darwin arm64"; do
    # shellcheck disable=SC2086 # deliberate word splitting on the pair
    set -- $platform
    write_fake_binary "$releases_dir/download/$version" "$version" "$1" "$2"
    archive=$releases_dir/download/$version/touchstone_${version}_${1}_${2}.tar.gz
    tar -czf "$archive" -C "$releases_dir/download/$version" touchstone
  done
  write_checksums "$version" correct
done

request_log=$tmp/requests.log
: >"$request_log"
port_file=$tmp/port
cat >"$tmp/server.py" <<PYEOF
import http.server
import sys

root, log_path, port_file = sys.argv[1], sys.argv[2], sys.argv[3]

class Handler(http.server.SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=root, **kwargs)

    def log_message(self, fmt, *args):
        with open(log_path, "a") as log:
            log.write(self.path + "\n")

    def serve_latest_redirect(self):
        self.send_response(302)
        self.send_header("Location", "/releases/tag/$RELEASE_V2")
        self.end_headers()

    def serve_tag_ok(self):
        self.send_response(200)
        self.end_headers()

    def do_GET(self):
        if self.path == "/releases/latest":
            return self.serve_latest_redirect()
        if self.path.startswith("/releases/tag/"):
            return self.serve_tag_ok()
        super().do_GET()

    def do_HEAD(self):
        if self.path == "/releases/latest":
            return self.serve_latest_redirect()
        if self.path.startswith("/releases/tag/"):
            return self.serve_tag_ok()
        super().do_HEAD()

server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
with open(port_file, "w") as out:
    out.write(str(server.server_address[1]) + "\n")
server.serve_forever()
PYEOF

# Serve the temp root so "/releases/download/..." resolves inside releases_dir.
python3 "$tmp/server.py" "$tmp" "$request_log" "$port_file" >"$tmp/server.out" 2>&1 &
server_pid=$!
# Wait for the server to accept connections on the port it selected.
i=0
while ! [ -s "$port_file" ] && [ "$i" -lt 50 ]; do
  sleep 0.1
  i=$((i + 1))
done
[ -s "$port_file" ] || fail_test "fake release server did not start"
port=$(cat "$port_file")
release_url="http://127.0.0.1:$port/releases"

# Success: pinned version installs the verified binary for the platform.
shim_dir=$tmp/shim-linux-amd64
make_uname_shim "$shim_dir" Linux x86_64
install_dir=$tmp/bin-success
run_installer "$shim_dir" "$install_dir" --version "$RELEASE_V1" ||
  fail_test "success case: installer exited non-zero"
[ -x "$install_dir/touchstone" ] || fail_test "success case: binary not installed or not executable"
installed=$("$install_dir/touchstone")
[ "$installed" = "touchstone $RELEASE_V1 linux/amd64" ] ||
  fail_test "success case: installed wrong binary, got $installed"
grep -q "checksum verified" "$tmp/out.log" || fail_test "success case: no checksum verification message"
grep -qF "/releases/download/$RELEASE_V1/touchstone_${RELEASE_V1}_linux_amd64.tar.gz" "$request_log" ||
  fail_test "success case: expected archive was not requested"
pass "success case installs the verified binary for linux/amd64"

# Version pin via environment variable selects a different release.
env_dir=$tmp/bin-env-pin
TOUCHSTONE_VERSION="$RELEASE_V2" run_installer "$shim_dir" "$env_dir" ||
  fail_test "env version pin: installer exited non-zero"
installed=$("$env_dir/touchstone")
[ "$installed" = "touchstone $RELEASE_V2 linux/amd64" ] ||
  fail_test "env version pin: installed wrong binary, got $installed"
pass "TOUCHSTONE_VERSION pins the installed release"

# Latest: without a pin the installer follows the release redirect.
latest_dir=$tmp/bin-latest
run_installer "$shim_dir" "$latest_dir" ||
  fail_test "latest release: installer exited non-zero"
installed=$("$latest_dir/touchstone")
[ "$installed" = "touchstone $RELEASE_V2 linux/amd64" ] ||
  fail_test "latest release: installed wrong binary, got $installed"
pass "no pin resolves the latest release through the redirect"

# Dry run: prints the plan and never talks to the release server.
dry_run_dir=$tmp/bin-dry-run
before=$(cat "$request_log")
run_installer "$shim_dir" "$dry_run_dir" --dry-run --version "$RELEASE_V1" ||
  fail_test "dry run: installer exited non-zero"
grep -q "dry run" "$tmp/out.log" || fail_test "dry run: no dry-run notice in output"
[ ! -e "$dry_run_dir" ] || fail_test "dry run: install directory was created"
[ "$(cat "$request_log")" = "$before" ] || fail_test "dry run: server received requests"
pass "dry run prints the plan without downloading or installing"

# Bad checksum: the mismatch aborts before anything is installed.
write_checksums "$RELEASE_V1" corrupt
bad_dir=$tmp/bin-bad-checksum
if run_installer "$shim_dir" "$bad_dir" --version "$RELEASE_V1"; then
  fail_test "bad checksum: installer unexpectedly succeeded"
fi
grep -q "checksum mismatch" "$tmp/out.log" || fail_test "bad checksum: no mismatch error"
[ ! -e "$bad_dir/touchstone" ] || fail_test "bad checksum: binary was installed anyway"
pass "bad checksum aborts the install cleanly"
write_checksums "$RELEASE_V1" correct

# Unsupported platform: the installer fails before any download.
sunos_dir=$tmp/shim-sunos
make_uname_shim "$sunos_dir" SunOS i86pc
if run_installer "$sunos_dir" "$tmp/bin-unsupported" --version "$RELEASE_V1"; then
  fail_test "unsupported platform: installer unexpectedly succeeded"
fi
grep -q "unsupported operating system: SunOS" "$tmp/out.log" ||
  fail_test "unsupported platform: no platform error message"
[ ! -e "$tmp/bin-unsupported" ] || fail_test "unsupported platform: install directory was created"
pass "unsupported platform fails before downloading"

printf 'all installer tests passed\n'
