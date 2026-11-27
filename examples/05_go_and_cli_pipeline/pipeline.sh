#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN="${ROOT}/bin/rulemma"

if [[ ! -x "${BIN}" ]]; then
  echo "CLI binary not found at ${BIN}. Build it first with: make build-cli" >&2
  exit 1
fi

printf "Купить зимние кроссовки Найк в Москве\nКроссовки на зиму Найк\nШина 35 см для бензопилы\n" \
  | "${BIN}" lemmatize --data "${ROOT}/data" --mode semkey --stopwords "в,на,для,купить,москва"
