#!/bin/bash
# Build and deploy the GoCamel documentation to a VPS.
#
# Usage:
#   ./deploy.sh                          # build only
#   ./deploy.sh user@vps:/var/www/gocamel # build + rsync
set -euo pipefail

cd "$(dirname "$0")"

echo "Building Hugo site..."
hugo build --minify

if [ $# -eq 1 ]; then
    DEST="$1"
    echo "Deploying to $DEST..."
    rsync -avz --delete public/ "$DEST/"
    echo "Done."
else
    echo "Build complete. Output in public/."
    echo "To deploy: ./deploy.sh user@vps:/var/www/gocamel"
fi