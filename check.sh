#!/usr/bin/env bash
# Local replacement for the CI pipeline: build, vet and test (with the race
# detector) every Go module of the repository, in parallel, then optionally
# lint them.
#
# Usage:
#   ./check.sh                      check every module
#   ./check.sh --lint               check every module, then run ./lint.sh
#   ./check.sh . components/redis   check only the given modules
#   JOBS=2 ./check.sh               limit the number of parallel modules
#
# Each module prints its output only once it is done, so parallel runs do not
# interleave. The exit status is non-zero if any module fails.
set -u

cd "$(dirname "$0")"

lint=0
modules=()
for arg in "$@"; do
  case "$arg" in
    --lint) lint=1 ;;
    -h | --help)
      sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) modules+=("${arg%/}") ;;
  esac
done

if [ ${#modules[@]} -eq 0 ]; then
  while IFS= read -r mod; do
    modules+=("$(dirname "$mod")")
  done < <(find . -name go.mod -not -path '*/.*' -not -path './hugo-site/*' | sort)
fi

# -race makes every test binary heavy: half the CPUs keeps the machine usable.
if command -v nproc >/dev/null 2>&1; then
  cpus=$(nproc)
elif command -v sysctl >/dev/null 2>&1; then
  cpus=$(sysctl -n hw.ncpu 2>/dev/null || echo 2)
else
  cpus=2
fi
default_jobs=$((cpus / 2))
[ "$default_jobs" -lt 1 ] && default_jobs=1
jobs="${JOBS:-$default_jobs}"

tmpdir=$(mktemp -d) || exit 1
trap 'rm -rf "$tmpdir"' EXIT
export tmpdir

check_module() {
  idx="$1"
  dir="$2"
  start=$(date +%s)
  out=$(cd "$dir" && go build ./... 2>&1 && go vet ./... 2>&1 && go test -race ./... 2>&1)
  status=$?
  elapsed=$(($(date +%s) - start))
  if [ $status -eq 0 ]; then
    printf 'ok    %-28s %3ss\n' "$dir" "$elapsed" >"$tmpdir/$idx.out"
  else
    printf 'FAIL  %-28s %3ss\n%s\n\n' "$dir" "$elapsed" "$out" >"$tmpdir/$idx.out"
  fi
  printf '%d\n' "$status" >"$tmpdir/$idx.rc"
  return 0
}
export -f check_module

echo "Checking ${#modules[@]} module(s), $jobs at a time..."
for i in "${!modules[@]}"; do
  printf '%s\0%s\0' "$i" "${modules[$i]}"
done | xargs -0 -n 2 -P "$jobs" bash -c 'check_module "$1" "$2"' _
xargs_status=$?

succeeded=0
for i in "${!modules[@]}"; do
  if [ -f "$tmpdir/$i.out" ]; then
    cat "$tmpdir/$i.out"
  fi
  if [ -f "$tmpdir/$i.rc" ] && [ "$(cat "$tmpdir/$i.rc" 2>/dev/null)" = "0" ]; then
    succeeded=$((succeeded + 1))
  fi
done

if [ "$xargs_status" -ne 0 ]; then
  echo "❌ check execution failed (xargs exit $xargs_status)."
  exit 1
fi
if [ "$succeeded" -ne "${#modules[@]}" ]; then
  failed=$((${#modules[@]} - succeeded))
  echo "❌ $failed module(s) failed."
  exit 1
fi
echo "✅ All modules passed."

if [ "$lint" -eq 1 ]; then
  ./lint.sh
fi
