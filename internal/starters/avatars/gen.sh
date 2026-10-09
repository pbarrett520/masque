#!/usr/bin/env bash
# Generates the three starter avatars with ImageMagick: Masque's velvet
# plum-black ground, one gilt emblem each, 2:3 portrait. Original,
# geometric, no likenesses. Re-run after editing; commit the PNGs.
set -euo pipefail
cd "$(dirname "$0")"
W=600; H=900
BG='#141116'      # velvet (dark surface)
GILT='#D4AF5A'    # gilt accent
DIM='#5a4b2e'     # gilt at low light
common=(-size ${W}x${H} "xc:$BG" -stroke "$GILT" -fill none -strokewidth 6)

# WREN: a ship's lens — concentric ring, one aperture, a horizon line.
convert "${common[@]}" \
  -draw "circle 300,420 300,230" \
  -stroke "$DIM" -strokewidth 3 -draw "circle 300,420 300,270" \
  -stroke "$GILT" -strokewidth 6 -draw "circle 300,420 300,330" \
  -fill "$GILT" -stroke none -draw "circle 300,420 300,400" \
  -fill "$BG" -draw "rectangle 285,398 315,442" \
  -fill none -stroke "$DIM" -strokewidth 3 -draw "line 90,700 510,700" \
  -stroke "$GILT" -strokewidth 3 -draw "line 230,700 370,700" \
  wren.png

# The Narrator: an open book, a small flame above the spine.
convert "${common[@]}" \
  -draw "path 'M 120,560 Q 300,500 480,560 L 480,700 Q 300,640 120,700 Z'" \
  -draw "line 300,530 300,670" \
  -stroke "$DIM" -strokewidth 3 \
  -draw "path 'M 160,590 Q 240,560 280,580'" -draw "path 'M 160,630 Q 240,600 280,620'" \
  -draw "path 'M 320,580 Q 360,560 440,590'" -draw "path 'M 320,620 Q 360,600 440,630'" \
  -stroke "$GILT" -strokewidth 6 -fill "$GILT" \
  -draw "path 'M 300,330 Q 345,400 300,450 Q 255,400 300,330 Z'" \
  -fill "$BG" -stroke none -draw "path 'M 300,380 Q 318,410 300,430 Q 282,410 300,380 Z'" \
  narrator.png

# Lǎo Zhāng: a teacup on a saucer, three lines of steam.
convert "${common[@]}" \
  -draw "path 'M 190,470 L 410,470 Q 395,640 300,650 Q 205,640 190,470 Z'" \
  -draw "path 'M 410,500 Q 470,500 465,550 Q 460,600 400,595'" \
  -draw "ellipse 300,690 150,22 0,360" \
  -stroke "$DIM" -strokewidth 4 \
  -draw "path 'M 250,420 Q 230,380 250,340 Q 270,300 250,260'" \
  -draw "path 'M 300,430 Q 280,390 300,350 Q 320,310 300,270'" \
  -draw "path 'M 350,420 Q 330,380 350,340 Q 370,300 350,260'" \
  laozhang.png
