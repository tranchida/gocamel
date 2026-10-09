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
default_jobs=$(($(nproc) / 2))
[ "$default_jobs" -lt 1 ] && default_jobs=1
jobs="${JOBS:-$default_jobs}"

check_module() {
  dir="$1"
  start=$(date +%s)
  out=$(cd "$dir" && go build ./... 2>&1 && go vet ./... 2>&1 && go test -race ./... 2>&1)
  status=$?
  elapsed=$(($(date +%s) - start))
  if [ $status -eq 0 ]; then
    printf 'ok    %-28s %3ss\n' "$dir" "$elapsed"
  else
    printf 'FAIL  %-28s %3ss\n%s\n\n' "$dir" "$elapsed" "$out"
  fi
  return $status
}
export -f check_module

echo "Checking ${#modules[@]} module(s), $jobs at a time..."
results=$(printf '%s\n' "${modules[@]}" | xargs -P "$jobs" -I{} bash -c 'check_module {}')
printf '%s\n' "$results"

failed=$(printf '%s\n' "$results" | grep -c '^FAIL')
if [ "$failed" -ne 0 ]; then
  echo "❌ $failed module(s) failed."
  exit 1
fi
echo "✅ All modules passed."

if [ "$lint" -eq 1 ]; then
  ./lint.sh
fi
