#!/usr/bin/env bash
# Build the decoders that aren't in Homebrew, at pinned commits, into
# ~/.local/share/sweep/bin (sdrd searches there first; override with -tool-dir).
#
#   rs1729/RS            radiosondes (rs41mod, dfm09mod, m10mod, lms6Xmod, ...) and
#                        mk2a1680mod (US 1680 MHz LMS6/MkIIa, IQ input)
#   mutability/dump978   UAT 978 (dump978, uat2json, uat2text)
#   lwvmobile/dsd-fme    DMR / D-STAR / YSF / NXDN digital voice
#
# Homebrew deps: brew install cmake libsndfile ncurses mbelib pulseaudio codec2
set -euo pipefail

SRC="${SWEEP_SRC:-$HOME/.local/src/sweep}"
BIN="${SWEEP_BIN:-$HOME/.local/share/sweep/bin}"
mkdir -p "$SRC" "$BIN"

fetch() { # repo dir commit
  local repo=$1 dir=$2 commit=$3
  if [ ! -d "$SRC/$dir/.git" ]; then
    git clone -q "https://github.com/$repo.git" "$SRC/$dir"
  fi
  git -C "$SRC/$dir" fetch -q origin "$commit" 2>/dev/null || git -C "$SRC/$dir" fetch -q --unshallow 2>/dev/null || true
  git -C "$SRC/$dir" checkout -q "$commit"
  echo "$repo @ $commit"
}

fetch rs1729/RS RS ce448555d34da5ca01f1a526e838c4f7be464481
make -C "$SRC/RS/demod/mod" CC=cc -j8 >/dev/null
cc -Ofast "$SRC/RS/mk2a/mk2a1680mod.c" -lm -o "$BIN/mk2a1680mod"
cp "$SRC"/RS/demod/mod/{rs41mod,dfm09mod,m10mod,lms6Xmod,mXXmod,imet54mod,rs92mod} "$BIN/"

fetch mutability/dump978 dump978 c7c4ad2f953692eadc5702aa8f059ea93fba2934
# upstream builds with -Werror; current clang warns about 1-bit bitfields that
# are only tested for non-zero, so build without -Werror.
make -C "$SRC/dump978" CC=cc CFLAGS="-O2 -g -Wall -Ifec" -j8 >/dev/null
cp "$SRC"/dump978/{dump978,uat2json,uat2text} "$BIN/"

fetch lwvmobile/dsd-fme dsd-fme a77609ab970925196e05f3f197b1050a98315564
NC=$(brew --prefix ncurses)
cmake -S "$SRC/dsd-fme" -B "$SRC/dsd-fme/build" -DCMAKE_BUILD_TYPE=Release \
  -DCMAKE_PREFIX_PATH="$(brew --prefix);$NC" -DCURSES_INCLUDE_PATH="$NC/include" \
  -DCURSES_LIBRARY="$NC/lib/libncursesw.dylib" >/dev/null
make -C "$SRC/dsd-fme/build" -j10 >/dev/null
cp "$SRC/dsd-fme/build/dsd-fme" "$BIN/"

echo "installed into $BIN:"
ls "$BIN"
