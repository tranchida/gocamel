#!/bin/bash
# Build and deploy the GoCamel documentation to the cloud server.
#
# The remote directory is written as root (sudo) and then handed over to the
# web server user, so `sudo` must be passwordless for the deploy user.
#
# Usage:
#   ./deploy.sh                              # build + deploy to cloud:/var/www/gocamel
#   ./deploy.sh --build-only                 # build only, output in public/
#   ./deploy.sh user@host:/path/to/dir       # build + deploy to a custom target
#
# Environment overrides:
#   DEPLOY_DEST=user@host:/path              # deployment target
#   DEPLOY_OWNER=caddy:caddy                 # owner applied after deploy ("" to skip)
#   DEPLOY_REMOTE_SUDO=0                     # disable sudo on the remote side
set -euo pipefail

cd "$(dirname "$0")"

DEFAULT_DEST="cloud:/var/www/gocamel"
DEST="${DEPLOY_DEST:-$DEFAULT_DEST}"
REMOTE_OWNER="${DEPLOY_OWNER-caddy:caddy}"
REMOTE_SUDO="${DEPLOY_REMOTE_SUDO:-1}"
BUILD_ONLY=0

case "${1:-}" in
    --build-only)
        BUILD_ONLY=1
        ;;
    "")
        ;;
    -*)
        echo "Unknown option: $1" >&2
        echo "Usage: ./deploy.sh [--build-only | user@host:/path]" >&2
        exit 1
        ;;
    *)
        DEST="$1"
        ;;
esac

echo "Building Hugo site..."
hugo build --minify

if [ "$BUILD_ONLY" -eq 1 ]; then
    echo "Build complete. Output in public/."
    exit 0
fi

if [[ "$DEST" == *:* ]]; then
    REMOTE="${DEST%%:*}"
    REMOTE_DIR="${DEST#*:}"

    if [ "$REMOTE_SUDO" = "1" ]; then
        echo "Preparing $REMOTE_DIR on $REMOTE..."
        ssh "$REMOTE" "sudo mkdir -p '$REMOTE_DIR'"
        echo "Deploying to $DEST..."
        rsync -avz --delete --rsync-path="sudo rsync" public/ "$DEST/"
        if [ -n "$REMOTE_OWNER" ]; then
            echo "Setting ownership to $REMOTE_OWNER..."
            ssh "$REMOTE" "sudo chown -R '$REMOTE_OWNER' '$REMOTE_DIR'"
        fi
    else
        echo "Deploying to $DEST..."
        rsync -avz --delete public/ "$DEST/"
    fi
else
    echo "Deploying to $DEST..."
    rsync -avz --delete public/ "$DEST/"
fi

echo "Done."
