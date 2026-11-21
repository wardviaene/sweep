#!/usr/bin/env bash
# Bulk-download the public datasets sweep uses to name what it receives
# (see docs/metadata.md). This is the ONLY place anything is downloaded:
# sdrd never makes per-aircraft, per-station or per-satellite requests.
#
#   SWEEP_DB=/some/dir scripts/fetch-metadata.sh     (default ~/.local/share/sweep/db)
#
# Safe to re-run: each file is replaced atomically and fetched.json records
# the download time of every dataset.
set -euo pipefail

DB="${SWEEP_DB:-$HOME/.local/share/sweep/db}"
UA="sweep-sdr-metadata"
command -v jq >/dev/null || { echo "jq is required (brew install jq)" >&2; exit 1; }
mkdir -p "$DB/faa" "$DB/satnogs" "$DB/tle"

now() { date -u +%Y-%m-%dT%H:%M:%SZ; }
size() { du -h "$1" | cut -f1; }
FETCHED="$DB/fetched.json"
[ -s "$FETCHED" ] || echo '{}' > "$FETCHED"

stamp() { # stamp <name>: record the download time
  local tmp; tmp="$(mktemp)"
  jq --arg k "$1" --arg v "$(now)" '.[$k] = $v' "$FETCHED" > "$tmp" && mv "$tmp" "$FETCHED"
}

get() { # get <url> <dest>
  local url="$1" dest="$2"
  curl --fail --silent --show-error --location --retry 2 --connect-timeout 20 --max-time 600 \
    -A "$UA" -o "$dest.tmp" "$url"
  mv "$dest.tmp" "$dest"
}

echo "sweep metadata -> $DB"

echo "airlines (OpenFlights, ODbL 1.0)"
get https://raw.githubusercontent.com/jpatokal/openflights/master/data/airlines.dat "$DB/airlines.dat"
stamp airlines; echo "  airlines.dat $(size "$DB/airlines.dat")"

echo "FAA releasable aircraft registry (public domain)"
tmpd="$(mktemp -d)"; trap 'rm -rf "$tmpd"' EXIT
get https://registry.faa.gov/database/ReleasableAircraft.zip "$tmpd/faa.zip"
# keep only what sweep reads; owner names and addresses are never extracted or stored
unzip -o -q -j "$tmpd/faa.zip" MASTER.txt ACFTREF.txt -d "$tmpd"
mv "$tmpd/MASTER.txt" "$DB/faa/MASTER.txt"; mv "$tmpd/ACFTREF.txt" "$DB/faa/ACFTREF.txt"
stamp faa; echo "  faa/MASTER.txt $(size "$DB/faa/MASTER.txt"), faa/ACFTREF.txt $(size "$DB/faa/ACFTREF.txt")"

echo "SatNOGS DB (CC BY-SA 4.0)"
get "https://db.satnogs.org/api/satellites/?format=json" "$DB/satnogs/satellites.json"
get "https://db.satnogs.org/api/transmitters/?format=json" "$DB/satnogs/transmitters.json"
stamp satnogs; echo "  satnogs/satellites.json $(size "$DB/satnogs/satellites.json"), transmitters.json $(size "$DB/satnogs/transmitters.json")"

echo "CelesTrak TLEs (one request per group)"
for g in amateur weather; do
  get "https://celestrak.org/NORAD/elements/gp.php?GROUP=$g&FORMAT=tle" "$DB/tle/$g.txt"
  stamp "tle_$g"; echo "  tle/$g.txt $(size "$DB/tle/$g.txt")"
  sleep 2
done
stamp tle
echo done
