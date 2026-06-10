#!/bin/bash

set -euo pipefail

cd $(dirname $0)/../

REGISTRY=${REGISTRY:-'docker.io'}
REPO=${REPO:-'library'}
TAG=${TAG:-'latest'}

# FYI: https://docs.docker.com/build/buildkit/toml-configuration/#buildkitdtoml
BUILDX_CONFIG_DIR=${BUILDX_CONFIG_DIR:-"$HOME/.config/buildkit/"}
BUILDX_CONFIG=${BUILDX_CONFIG:-"$HOME/.config/buildkit/buildkitd.toml"}
BUILDX_OPTIONS=${BUILDX_OPTIONS:-''} # Set to '--push' to upload images

if [[ ! -e "${BUILDX_CONFIG}" ]]; then
    mkdir -p ${BUILDX_CONFIG_DIR}
    touch ${BUILDX_CONFIG}
fi

echo "Start local build images for local dev purpose"

docker build \
    -f package/Dockerfile \
    -t "${REGISTRY}/${REPO}/retro-mcp:${TAG}" \
    .

echo "Image: Done"
