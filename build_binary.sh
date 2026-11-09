#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"

if [ ! -d .venv ]; then
    python3 -m venv .venv
fi

.venv/bin/pip install --quiet -r requirements-dev.txt
.venv/bin/pyinstaller --onefile --name knigojor --clean --noconfirm main.py

echo "Binary built at dist/knigojor"
