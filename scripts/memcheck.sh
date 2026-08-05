#!/usr/bin/env bash
#
# Measure what Waxgrove actually costs in memory, under load, in the container.
#
# The question this answers is "does it fit on the 961 MB target host without
# adding RAM", and it has to be re-runnable as the project grows — a figure
# taken once at M1 is worthless by M3.
#
# Two metrics, because they answer different questions:
#
#   anon  - memory that CANNOT be reclaimed under pressure. This is the number
#           that decides whether the host needs more RAM.
#   peak  - the cgroup's high-water mark, which INCLUDES page cache. Page cache
#           is reclaimable, so a large peak on an idle host is not pressure.
#           Reported because it is what `docker stats` shows people, and the
#           gap between the two is worth seeing.
#
# Argon2id is the interesting load: 64 MiB per password hash, by design. Idle
# memory is not the risk; a burst of concurrent logins is.
#
# Usage:
#   scripts/memcheck.sh                 # all scenarios at the default limit
#   scripts/memcheck.sh --limit 128m    # can it survive a tighter cap?
#   scripts/memcheck.sh --only logins
#
set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

# Matches the default in compose.yaml. Changing one without the other is how a
# measured figure quietly stops describing what actually ships.
LIMIT="${WAXGROVE_MEM_LIMIT:-256m}"
PORT="${WAXGROVE_HOST_PORT:-8093}"
ONLY=""
SAMPLE_MS=50

while [ $# -gt 0 ]; do
  case "$1" in
    --limit) LIMIT="$2"; shift 2 ;;
    --port)  PORT="$2";  shift 2 ;;
    --only)  ONLY="$2";  shift 2 ;;
    -h|--help) sed -n '2,30p' "$0"; exit 0 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

BASE="http://127.0.0.1:${PORT}"
PW="correct-horse-battery-staple"
JAR="$(mktemp -d)/cookies"
trap 'rm -rf "$(dirname "$JAR")"' EXIT

say()  { printf '\n\033[1m== %s\033[0m\n' "$*"; }
note() { printf '   %s\n' "$*"; }

# --- container control -------------------------------------------------------

cgroup_path() {
  local cid
  cid="$(docker ps -qf name=waxgrove-waxgrove-1 --no-trunc 2>/dev/null)"
  [ -n "$cid" ] || return 1
  local p="/sys/fs/cgroup/system.slice/docker-${cid}.scope"
  [ -d "$p" ] || p="/sys/fs/cgroup/docker/${cid}"
  [ -d "$p" ] || return 1
  echo "$p"
}

read_anon() {
  local p="$1"
  awk '/^anon /{print $2; exit}' "$p/memory.stat" 2>/dev/null || echo 0
}

mib() { awk -v b="${1:-0}" 'BEGIN{printf "%.1f", b/1048576}'; }

# Build once, up front. Without this the scenarios below run whatever image
# happens to be lying around, which silently measures the previous version of
# the code — the single most misleading thing this script could do.
build_image() {
  say "Building the image from the current tree"
  docker compose build >/dev/null 2>&1 \
    || { echo "docker compose build failed" >&2; exit 1; }
  note "built"
}

# A fresh container per scenario, so the cgroup's peak belongs to that scenario
# alone. memory.peak is not resettable without root, which is why this is a
# restart rather than a counter reset.
restart_clean() {
  docker compose down -v >/dev/null 2>&1
  WAXGROVE_MEM_LIMIT="$LIMIT" WAXGROVE_HOST_PORT="$PORT" \
    docker compose up -d --no-build >/dev/null 2>&1 || { echo "compose up failed" >&2; exit 1; }
  for _ in $(seq 1 60); do
    curl -fsS "$BASE/health" >/dev/null 2>&1 && return 0
    sleep 0.5
  done
  echo "server never became healthy" >&2
  docker compose logs --tail 20
  exit 1
}

# Samples anon memory while a workload runs, and records the maximum seen.
# Sampling (rather than reading the kernel's peak) is what isolates anon from
# page cache; at 50ms it comfortably catches an Argon2 hash, which takes
# roughly an order of magnitude longer.
#
# Deliberately NOT wrapped in a command substitution. A backgrounded sampler
# inherits the substitution's pipe, so `$(...)` blocks until the sampler exits
# even after the workload is done — which reads as a hang, not a bug.
# Everything here communicates through files and a global instead.
SAMPLED_MAX=0

sampler_loop() {
  local p="$1" stopfile="$2" max=0 outfile="$3" cur
  while [ -e "$stopfile" ]; do
    cur="$(read_anon "$p")"
    if [ "${cur:-0}" -gt "$max" ] 2>/dev/null; then max="$cur"; fi
    sleep "0.$(printf '%03d' "$SAMPLE_MS")"
  done
  echo "$max" > "$outfile"
}

with_sampling() {
  local p="$1"; shift
  local stopfile maxfile
  stopfile="$(mktemp)"
  maxfile="$(mktemp)"

  sampler_loop "$p" "$stopfile" "$maxfile" &
  local sampler=$!

  # In a subshell, so that a bare `wait` inside a workload sees only its own
  # background curls. Run directly, it would also wait for the sampler above —
  # which does not exit until the workload returns. That deadlocks.
  ( "$@" )

  rm -f "$stopfile"
  wait "$sampler" 2>/dev/null
  SAMPLED_MAX="$(cat "$maxfile" 2>/dev/null || echo 0)"
  rm -f "$maxfile"
}

report() {
  local label="$1" p="$2" peak_anon="$3"
  local cg_peak settled oom high restarts limit_bytes pct
  cg_peak="$(cat "$p/memory.peak" 2>/dev/null || echo 0)"
  settled="$(read_anon "$p")"
  oom="$(awk '/^oom_kill /{print $2}' "$p/memory.events" 2>/dev/null || echo 0)"
  # `high` counts times the cgroup pushed back on allocation. Non-zero means
  # the container was reclaiming under pressure to stay alive, which survives
  # but is not headroom.
  high="$(awk '/^high /{print $2}' "$p/memory.events" 2>/dev/null || echo 0)"
  limit_bytes="$(cat "$p/memory.max" 2>/dev/null || echo 0)"
  restarts="$(docker inspect "$(docker ps -qf name=waxgrove-waxgrove-1)" \
              --format '{{.RestartCount}}' 2>/dev/null || echo '?')"

  pct='?'
  if [ "${limit_bytes:-0}" -gt 0 ] 2>/dev/null; then
    pct="$(awk -v a="$cg_peak" -v b="$limit_bytes" 'BEGIN{printf "%d", (a*100)/b}')"
  fi

  printf '   %-22s anon peak %7s MiB | anon now %7s MiB | cgroup peak %7s MiB (%s%% of limit)' \
    "$label" "$(mib "$peak_anon")" "$(mib "$settled")" "$(mib "$cg_peak")" "$pct"
  if [ "${oom:-0}" != "0" ]; then
    printf ' | \033[31mOOM-KILLED x%s\033[0m' "$oom"
  fi
  if [ "${restarts:-0}" != "0" ] && [ "${restarts:-?}" != "?" ]; then
    printf ' | \033[31mRESTARTED x%s\033[0m' "$restarts"
  fi
  # Anything above this is surviving by reclaiming, not by having room.
  if [ "${pct:-0}" != '?' ] && [ "${pct:-0}" -ge 90 ] 2>/dev/null; then
    printf ' | \033[33mAT THE CEILING\033[0m'
  fi
  printf '\n'
}

# --- workloads ---------------------------------------------------------------

# The first account registered becomes the admin, and needs no invite.
seed_user() {
  curl -fsS -c "$JAR" -X POST "$BASE/api/register" \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"bench@example.test\",\"display_name\":\"Bench\",\"password\":\"$PW\",\"invite_code\":\"\"}" \
    >/dev/null 2>&1
}

# Long enough to cross the auth scavenger's quiet period (see
# internal/auth/scavenge.go), because the settled figure is the one that
# decides host sizing — not the transient peak.
w_idle() { sleep 26; }

# The load that matters. Every login is a 64 MiB Argon2id hash; N at once is
# N x 64 MiB live at the same moment, which is the one thing that can put this
# over a small cap.
w_logins() {
  local n="$1" i
  for i in $(seq 1 "$n"); do
    curl -fsS -o /dev/null -X POST "$BASE/api/login" \
      -H 'Content-Type: application/json' \
      -d "{\"email\":\"bench@example.test\",\"password\":\"$PW\"}" &
  done
  wait
}

w_catalogue() {
  local n="${1:-200}" i body
  local pl
  pl="$(curl -fsS -b "$JAR" -X POST "$BASE/api/playlists" \
        -H 'Content-Type: application/json' \
        -d '{"title":"bench","description":""}' | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
  # One request carrying many candidates: the shape a real JSPF import takes.
  body='{"candidates":['
  for i in $(seq 1 "$n"); do
    [ "$i" -gt 1 ] && body+=','
    body+="{\"title\":\"Track $i\",\"artist\":\"Artist $((i % 40))\",\"isrc\":\"XX$(printf '%08d' "$i")\"}"
  done
  body+=']}'
  curl -fsS -o /dev/null -b "$JAR" -X POST "$BASE/api/playlists/$pl/tracks" \
    -H 'Content-Type: application/json' -d "$body"
}

w_search() {
  local i
  for i in $(seq 1 60); do
    curl -fsS -o /dev/null -b "$JAR" "$BASE/api/records?q=Artist%20$((i % 40))" &
    [ $((i % 12)) -eq 0 ] && wait
  done
  wait
}

w_shell() {
  local i
  for i in $(seq 1 40); do
    curl -fsS -o /dev/null "$BASE/" &
    curl -fsS -o /dev/null "$BASE/manifest.webmanifest" &
    [ $((i % 10)) -eq 0 ] && wait
  done
  wait
}

# --- scenarios ---------------------------------------------------------------

scenario() {
  local name="$1"; shift
  [ -n "$ONLY" ] && [ "$ONLY" != "$name" ] && return 0

  restart_clean
  local p; p="$(cgroup_path)" || { echo "   $name: cgroup not found, skipped" >&2; return 1; }
  seed_user
  with_sampling "$p" "$@"
  report "$name" "$p" "$SAMPLED_MAX"
}

say "Waxgrove memory check   limit=$LIMIT  port=$PORT"
note "anon = unreclaimable, the figure that decides host sizing"
note "cgroup peak includes page cache, which is reclaimable"

build_image
echo

# Search needs something to search, so it seeds first and measures both.
w_catalogue_then_search() { w_catalogue 200; w_search; }

scenario idle            w_idle
scenario "serve-app-x40" w_shell
scenario "catalogue-200" w_catalogue 200
scenario "cat+search-60" w_catalogue_then_search
scenario "logins-x1"     w_logins 1
scenario "logins-x4"     w_logins 4
scenario "logins-x8"     w_logins 8
scenario "logins-x16"    w_logins 16

say "Done"
note "Target host: 961 MB total, ~439 MB available, swap 403/511 used."
note "Compare anon peak against what you are willing to give it there."
docker compose down -v >/dev/null 2>&1
