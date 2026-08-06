#!/usr/bin/env bash
# build-core-image.sh — build a Docker image of the docs-core server (the RBAC epic's paired core
# branch) so the Go e2e suite (server/e2e/) can boot a real Mattermost with the plugin installed via
# Testcontainers, without waiting for the branch to merge and ship a published image.
#
# What it does:
#   1. Guards that MM_SERVER_REPO is checked out on DOCS_CORE_BRANCH (never checks out for you).
#   2. Cross-compiles ./cmd/mattermost for the Docker daemon's architecture (so this works
#      whether Docker is running amd64 or arm64 containers, independent of the host arch).
#   3. Assembles a minimal build context (binary + i18n/templates/fonts from the core tree, plus
#      empty config/client/plugins/data/logs directories) in a temp dir.
#   4. Builds the image and tags it $CORE_IMAGE (default mm-docs-rbac-core:dev).
#
# Usage:
#   ./build/build-core-image.sh
#   CORE_IMAGE=my-tag:dev ./build/build-core-image.sh
#   ./build/build-core-image.sh --skip-build   # reuse the existing binaries at $MM_SERVER_REPO/bin/{mattermost,mmctl}-e2e-linux-<arch>
#
# Requires:
#   - Docker running locally.
#   - The core repo checked out on DOCS_CORE_BRANCH (this script refuses to run otherwise — it
#     never switches branches for you; see start-docs-core-server.sh for the same guard style).

set -euo pipefail

# Configuration is read from the environment with committed defaults: this script must not depend
# on any developer's local, untracked setup. The local bash suites keep their own env.sh, which
# exports these same names, so sourcing it beforehand also works.
#
#   MM_SERVER_REPO    path to the core repo's server/ directory (required; auto-detected from the
#                     conventional sibling checkouts when unset)
#   DOCS_CORE_BRANCH  branch the core repo must be on (default below)
#   CORE_IMAGE        image tag to build (default below)
DOCS_CORE_BRANCH="${DOCS_CORE_BRANCH:-MM-69269-permissions-rbac-core}"
CORE_IMAGE="${CORE_IMAGE:-mm-docs-rbac-core:dev}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if [[ -z "${MM_SERVER_REPO:-}" ]]; then
    for candidate in "$REPO_ROOT/../MM-69269-core/server" "$REPO_ROOT/../mattermost/server"; do
        if [[ -d "$candidate" ]]; then
            MM_SERVER_REPO="$(cd "$candidate" && pwd)"
            break
        fi
    done
fi
if [[ -z "${MM_SERVER_REPO:-}" || ! -d "$MM_SERVER_REPO" ]]; then
    echo "ERROR: cannot locate the core repo. Set MM_SERVER_REPO to its server/ directory, e.g." >&2
    echo "       MM_SERVER_REPO=/path/to/mattermost/server $0" >&2
    exit 1
fi

SKIP_BUILD=false
for arg in "$@"; do
    [[ "$arg" == "--skip-build" ]] && SKIP_BUILD=true
done

# ── Guard: core repo must be on the docs core-complement branch ─────────────────
# We never check out for you: the core repo holds uncommitted work, and silently switching
# branches could lose or mix it (see git-safety rules).
CURRENT_BRANCH="$(git -C "$MM_SERVER_REPO" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
if [[ "$CURRENT_BRANCH" != "$DOCS_CORE_BRANCH" ]]; then
    echo "ERROR: core repo at $MM_SERVER_REPO is on '$CURRENT_BRANCH', not '$DOCS_CORE_BRANCH'." >&2
    echo "       Check it out first:  git -C \"$MM_SERVER_REPO\" checkout $DOCS_CORE_BRANCH" >&2
    exit 1
fi

# ── Report drift from the committed core pin ────────────────────────────────────
# build/core-commit.txt is what CI holds CORE_IMAGE to. Building ahead of it is the normal way to
# develop a paired change, so this reports rather than refuses — but the pin has to be bumped
# before CI will accept an image built from the newer commit.
PINNED_COMMIT="$(grep -vE '^\s*(#|$)' "$REPO_ROOT/build/core-commit.txt" | head -1 | tr -d '[:space:]')"
CORE_HEAD="$(git -C "$MM_SERVER_REPO" rev-parse HEAD)"
if [[ "$CORE_HEAD" != "$PINNED_COMMIT" ]]; then
    echo "NOTE: core is at $CORE_HEAD but build/core-commit.txt pins $PINNED_COMMIT." >&2
    echo "      Bump the pin once this image is the one CI should run against." >&2
fi

# ── Detect the Docker daemon's architecture ─────────────────────────────────────
# Cross-compile for the arch the *daemon* runs containers as, not the host arch — the two can
# differ (e.g. Docker Desktop on Apple Silicon set to emulate amd64).
DOCKER_ARCH="$(docker info --format '{{.Architecture}}' 2>/dev/null || true)"
case "$DOCKER_ARCH" in
    aarch64) GOARCH_TARGET="arm64" ;;
    x86_64)  GOARCH_TARGET="amd64" ;;
    *)
        echo "ERROR: could not determine Docker daemon architecture (got '$DOCKER_ARCH'). Is Docker running?" >&2
        exit 1
        ;;
esac
echo "Docker daemon architecture: $DOCKER_ARCH -> GOARCH=$GOARCH_TARGET"

# ── Cross-compile the server + mmctl binaries ────────────────────────────────────
# mmctl is required in the image too: testcontainers-mattermost-go drives container setup
# (CreateAdmin, CreateTeam, AddUserToTeam, InstallPlugin, SetConfig) entirely via
# `mmctl --local ...` exec'd inside the container, not the HTTP API.
BINARY="$MM_SERVER_REPO/bin/mattermost-e2e-linux-$GOARCH_TARGET"
MMCTL_BINARY="$MM_SERVER_REPO/bin/mmctl-e2e-linux-$GOARCH_TARGET"
if [[ "$SKIP_BUILD" == true && -f "$BINARY" && -f "$MMCTL_BINARY" ]]; then
    echo "Skipping build (--skip-build), reusing $BINARY and $MMCTL_BINARY."
else
    echo "Cross-compiling docs-core server + mmctl for linux/$GOARCH_TARGET from branch $DOCS_CORE_BRANCH (this can take a minute)..."
    ( cd "$MM_SERVER_REPO" && env GOOS=linux GOARCH="$GOARCH_TARGET" CGO_ENABLED=0 \
        go build -o "$BINARY" ./cmd/mattermost )
    ( cd "$MM_SERVER_REPO" && env GOOS=linux GOARCH="$GOARCH_TARGET" CGO_ENABLED=0 \
        go build -o "$MMCTL_BINARY" ./cmd/mmctl )
    echo "Build complete: $BINARY, $MMCTL_BINARY"
fi

# ── Assemble the build context ───────────────────────────────────────────────────
BUILD_CTX="$(mktemp -d)"
trap 'rm -rf "$BUILD_CTX"' EXIT

MM_ROOT="$BUILD_CTX/mattermost"
mkdir -p "$MM_ROOT/bin" "$MM_ROOT/config" "$MM_ROOT/client/plugins" "$MM_ROOT/plugins" "$MM_ROOT/data" "$MM_ROOT/logs"
cp "$BINARY" "$MM_ROOT/bin/mattermost"
cp "$MMCTL_BINARY" "$MM_ROOT/bin/mmctl"
cp -R "$MM_SERVER_REPO/i18n" "$MM_ROOT/i18n"
cp -R "$MM_SERVER_REPO/templates" "$MM_ROOT/templates"
cp -R "$MM_SERVER_REPO/fonts" "$MM_ROOT/fonts"

cat > "$BUILD_CTX/Dockerfile" <<'DOCKEREOF'
# Pinned by digest: this image is the server the e2e scenarios assert against, so an upstream
# repaint of the noble tag would change behaviour between two builds of identical source.
FROM ubuntu:noble@sha256:561618e2c15bf2397621dd04f96926663a3b5616c189cf7e38db7e82f5c538ea

# Ubuntu's default sources use plain HTTP, which some networks refuse outright. Fetch over HTTPS
# instead. The base image carries no CA bundle until ca-certificates lands, so peer verification is
# off for this one transaction; package integrity still rests on apt's signature check of the
# repository metadata, as it does over plain HTTP.
RUN sed -i 's|http://|https://|g' /etc/apt/sources.list.d/ubuntu.sources \
    && echo 'Acquire::https::Verify-Peer "false";' > /etc/apt/apt.conf.d/99bootstrap \
    && apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    media-types \
    mailcap \
    tzdata \
    curl \
    && rm -f /etc/apt/apt.conf.d/99bootstrap \
    && rm -rf /var/lib/apt/lists/*

RUN groupadd -g 2000 mattermost && useradd -u 2000 -g 2000 -m mattermost

COPY --chown=2000:2000 mattermost /mattermost

ENV PATH="/mattermost/bin:${PATH}"
ENV MM_SERVICESETTINGS_ENABLELOCALMODE="true"

USER mattermost
WORKDIR /mattermost

EXPOSE 8065
CMD ["mattermost", "server"]
DOCKEREOF

# ── Build the image ───────────────────────────────────────────────────────────────
IMAGE_TAG="$CORE_IMAGE"
echo "Building Docker image $IMAGE_TAG from $BUILD_CTX ..."
docker build -t "$IMAGE_TAG" "$BUILD_CTX"

echo "$IMAGE_TAG"
