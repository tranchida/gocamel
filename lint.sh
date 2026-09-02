#!/usr/bin/env bash
set -e

# Point to Go 1.26.0 installed via mise
GOROOT="$HOME/.local/share/mise/installs/go/1.26.0"
export GOROOT
export PATH="$GOROOT/bin:$PATH"
export GOPATH="$(pwd)/.cache/go"
export PATH="$GOPATH/bin:$PATH"

STATICCHECK_VERSION="2025.1.1"
GOSEC_VERSION="v2.22.4"

if ! command -v staticcheck >/dev/null 2>&1; then
  echo "Installing staticcheck ${STATICCHECK_VERSION}..."
  go install "honnef.co/go/tools/cmd/staticcheck@${STATICCHECK_VERSION}"
fi

if ! command -v gosec >/dev/null 2>&1; then
  echo "Installing gosec ${GOSEC_VERSION}..."
  go install "github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}"
fi

has_errors=0

for mod in $(find . -name go.mod -not -path '*/.*' -not -path './hugo-site/*' | sort); do
  dir=$(dirname "$mod")
  echo "== staticcheck $dir =="
  (cd "$dir" && staticcheck ./...) || has_errors=1

  echo "== gosec $dir =="
  gosec_opts="-exclude-dir=_test"
  if [ "$dir" = "." ]; then
    gosec_opts="$gosec_opts -exclude-dir=components -exclude-dir=examples -exclude-dir=observability -exclude-dir=.cache -exclude-dir=.claude"
  fi
  (cd "$dir" && gosec $gosec_opts -severity medium ./...) || has_errors=1
done

if [ $has_errors -eq 0 ]; then
  echo "✅ All linters passed with 0 issues!"
else
  echo "❌ Linter issues detected."
  exit 1
fi
