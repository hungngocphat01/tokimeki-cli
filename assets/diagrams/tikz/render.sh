#!/usr/bin/env bash
# Render every figure (all .tex except style.tex) to ../../<name>.svg.
# Glyphs become paths, so the SVGs need no fonts.
set -euo pipefail

cd "$(dirname "$0")"
for tex in *.tex; do
    name="${tex%.tex}"
    [ "$name" = style ] && continue
    latex -interaction=nonstopmode -halt-on-error "$tex" > /dev/null || { grep -m3 '^!' "$name.log"; exit 1; }
    dvisvgm --no-fonts --exact-bbox --output="../../$name.svg" "$name.dvi" 2> /dev/null
    rm -f "$name.aux" "$name.log" "$name.dvi"
done
