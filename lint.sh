#!/usr/bin/env bash
set -e

# Use Go 1.27+ from PATH; go.mod and .go-version select the project default.
export GOPATH="$(pwd)/.cache/go"
export PATH="$GOPATH/bin:$PATH"

# staticcheck and gosec are built from .tools/lint/go.mod, which pins the
# golang.org/x/tools they use to read the compiler's export data. A linter can
# only read export data written by a Go no newer than its x/tools supports, so
# bump x/tools there when a new Go release breaks the linters, and rebuild the
# binaries whenever the Go version changes.
GO_VERSION="$(go env GOVERSION)"
STAMP="$GOPATH/bin/.built-with-go"
if [ "$(cat "$STAMP" 2>/dev/null)" != "$GO_VERSION" ] ||
  [ ! -x "$GOPATH/bin/staticcheck" ] || [ ! -x "$GOPATH/bin/gosec" ]; then
  echo "Installing staticcheck and gosec with ${GO_VERSION}..."
  (cd .tools/lint && GOWORK=off GOBIN="$GOPATH/bin" go install tool)
  echo "$GO_VERSION" > "$STAMP"
fi

has_errors=0

for mod in $(find . -name go.mod -not -path '*/.*' -not -path './hugo-site/*' | sort); do
  dir=$(dirname "$mod")
  echo "== staticcheck $dir =="
  (cd "$dir" && staticcheck ./...) || has_errors=1

  echo "== gosec $dir =="
  gosec_opts="-exclude-dir=_test"
  if [ "$dir" = "." ]; then
    gosec_opts="$gosec_opts -exclude-dir=components -exclude-dir=examples -exclude-dir=observability -exclude-dir=.cache -exclude-dir=.claude -exclude-dir=.tools"
  fi
  (cd "$dir" && gosec $gosec_opts -severity medium ./...) || has_errors=1
done

if [ $has_errors -eq 0 ]; then
  echo "✅ All linters passed with 0 issues!"
else
  echo "❌ Linter issues detected."
  exit 1
fi
