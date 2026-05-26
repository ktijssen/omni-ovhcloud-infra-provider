#!/usr/bin/env bash

set -e

RELEASE_TOOL_IMAGE="ghcr.io/siderolabs/release-tool:latest"

function release-tool {
  docker pull "${RELEASE_TOOL_IMAGE}" >/dev/null
  docker run --rm -w /src -v "${PWD}":/src:ro "${RELEASE_TOOL_IMAGE}" -l -d -n -t "${1}" ./hack/release.toml
}

function changelog {
  if [ "$#" -ne 1 ]; then
    echo 1>&2 "Usage: $0 changelog <tag>"
    exit 1
  fi
  touch CHANGELOG.md
  (release-tool "${1}"; echo; cat CHANGELOG.md) > CHANGELOG.md- && mv CHANGELOG.md- CHANGELOG.md
}

function release-notes {
  if [ "$#" -ne 2 ]; then
    echo 1>&2 "Usage: $0 release-notes <output-file> <tag>"
    exit 1
  fi
  release-tool "${2}" > "${1}"
}

if declare -f "$1" > /dev/null
then
  cmd="$1"
  shift
  $cmd "$@"
else
  cat <<EOF
Usage:
  changelog:     Prepend release notes for the given tag to CHANGELOG.md.
  release-notes: Generate release notes for a GitHub release.

Examples:
  $0 release-notes _out/RELEASE_NOTES.md v1.2.3
  $0 changelog v1.2.3
EOF
  exit 1
fi
