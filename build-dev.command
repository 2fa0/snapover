#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"
SNAPOVER_BUILD_DEV=1 exec ./build.command
