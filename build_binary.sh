#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

make build

echo "Binary built at bin/knigojor"
