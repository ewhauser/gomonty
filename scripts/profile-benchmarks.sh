#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-/tmp/gomonty-bench-profiles}"

mkdir -p "$OUT_DIR"

run_profile() {
  local name="$1"
  local bench="$2"
  local cpu_profile="$OUT_DIR/$name.cpu.pprof"
  local mem_profile="$OUT_DIR/$name.mem.pprof"
  local cpu_top="$OUT_DIR/$name.cpu.txt"
  local mem_top="$OUT_DIR/$name.mem.txt"

  echo "Profiling $name"
  go test \
    -run '^$' \
    -bench "$bench" \
    -benchtime=5s \
    -cpuprofile "$cpu_profile" \
    -memprofile "$mem_profile" \
    "$ROOT_DIR"

  go tool pprof -top "$cpu_profile" >"$cpu_top"
  go tool pprof -top -alloc_space "$mem_profile" >"$mem_top"
}

run_profile "end_to_end" "BenchmarkMontyEndToEnd$"
run_profile "heavy_pure" "BenchmarkMonty/list_append_int$"
run_profile "callback_heavy" "BenchmarkMontyCallbacks/external_loop$"

echo "Wrote profiles to $OUT_DIR"
