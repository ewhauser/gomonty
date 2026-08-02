#!/usr/bin/env bash
set -euo pipefail

# Rust selects the shared libgcc unwind runtime when crt-static is disabled for
# a musl cdylib. Shipping that hidden dependency would make a prepared runtime
# fail on a clean Alpine installation. Keep musl dynamic, but replace libgcc_s
# with the compiler's static builtins and unwind archives. Repeat libgcc after
# libc because libc may introduce late architecture helper references.
linker="${GOMONTY_MUSL_CC:-}"
if [[ -z "$linker" ]]; then
  echo "GOMONTY_MUSL_CC must name the underlying musl C compiler" >&2
  exit 1
fi

rewritten=()
rewrote_unwind=0
for argument in "$@"; do
  if [[ "$argument" == "-lgcc_s" ]]; then
    rewritten+=("-Wl,-Bstatic" "-lgcc" "-lgcc_eh" "-Wl,-Bdynamic")
    rewrote_unwind=1
  elif [[ "$argument" == "-lc" && "$rewrote_unwind" == "1" ]]; then
    rewritten+=("-lc" "-Wl,-Bstatic" "-lgcc" "-Wl,-Bdynamic")
  else
    rewritten+=("$argument")
  fi
done

exec "$linker" "${rewritten[@]}"
