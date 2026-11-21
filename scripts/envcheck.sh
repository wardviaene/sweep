#!/usr/bin/env bash
# Milestone 1 env check: host, dongle, tuner, rtl_test sample check, installed tools.
# Read-only: installs nothing. Exits 1 if no RTL-SDR device is found.
set -u

SECS="${ENVCHECK_SECS:-3}"
RATE="${ENVCHECK_RATE:-2048000}"
status=0

hdr() { printf '\n== %s\n' "$1"; }

# Run a command for N seconds, then stop it (macOS has no coreutils timeout).
run_for() {
  local secs=$1; shift
  "$@" 2>&1 &
  local pid=$!
  ( sleep "$secs"; kill -INT "$pid" 2>/dev/null ) &
  local killer=$!
  wait "$pid" 2>/dev/null
  kill "$killer" 2>/dev/null
  wait "$killer" 2>/dev/null
}

hdr "host"
printf 'arch:  %s\n' "$(uname -m)"
printf 'cpu:   %s\n' "$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo unknown)"
printf 'macOS: %s\n' "$(sw_vers -productVersion 2>/dev/null || echo unknown)"

hdr "dongle"
if ! command -v rtl_test >/dev/null; then
  echo "rtl_test not found (brew install librtlsdr)"
  exit 1
fi
probe="$(run_for 4 rtl_test -t)"
if ! grep -q "Found [1-9]" <<<"$probe"; then
  echo "no RTL-SDR device found"
  echo "$probe" | sed 's/^/  /'
  status=1
else
  device="$(grep -E '^ +0:' <<<"$probe" | sed -E 's/^ +0: +//')"
  tuner="$(grep -E '^Found .* tuner' <<<"$probe" | sed -E 's/^Found //')"
  gains="$(grep -E '^Supported gain values' <<<"$probe")"
  printf 'device: %s\n' "${device:-unknown}"
  printf 'tuner:  %s\n' "${tuner:-unknown}"
  printf 'gains:  %s\n' "${gains#Supported gain values }"
  case "$device" in
    *"SMArt v5"*) echo "hf:     direct sampling supported by the NESDR SMArt v5 (verify on air)";;
    *) case "$tuner" in
         *R820T*|*R828D*|*R860*) echo "hf:     unknown model; needs direct-sampling mod/V3/V4 or an upconverter";;
       esac;;
  esac

  hdr "rtl_test sample check (${SECS}s @ ${RATE} S/s)"
  out="$(run_for "$SECS" rtl_test -s "$RATE")"
  lost="$(grep -c 'lost at least' <<<"$out")"
  grep -E 'Sampling at|Reading samples' <<<"$out"
  if [ "$lost" -eq 0 ]; then
    echo "result: OK, no dropped samples"
  else
    echo "result: WARN, $lost dropped-sample reports"
    grep 'lost at least' <<<"$out" | head -5 | sed 's/^/  /'
  fi
fi

hdr "tools"
for t in rtl_tcp rtl_sdr rtl_fm rtl_433 dump1090 readsb direwolf sox play rigctl go node npm; do
  p="$(command -v "$t" 2>/dev/null)"
  printf '%-9s %s\n' "$t" "${p:-MISSING}"
done
printf '\ngo:   %s\n' "$(go version 2>/dev/null | awk '{print $3}')"
printf 'node: %s\n' "$(node --version 2>/dev/null)"
printf 'rtl_433: %s\n' "$(rtl_433 -V 2>&1 | head -1)"

exit "$status"
