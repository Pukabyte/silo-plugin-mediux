# Complete series artwork browsing

Goal: Every returned image stays reachable in Artwork Manager, separated by image type and season with horizontal scrolling. Preserve preview/Apply image/Apply set and remote source behavior.

Reference: AURA `frontend/src/components/shared/carousel-display.tsx` groups title cards and season posters by season metadata. Use native overflow scrolling and arrow buttons in this dependency-free plugin; do not copy AURA's limited titlecard sample.

Cause: `setCatalog` counted all artwork but returned at most eight images per kind.

- [x] Replace per-kind truncation with bounded 1000-image pages and automatic page merging.
- [x] Separate Posters, Backdrops, Logos, season posters, and title cards; group season artwork and order episodes numerically.
- [x] Add horizontal touch/keyboard scrolling, labeled arrow controls and per-row counts.
- [x] Verify 61-image three-season fixture, late-image preview without writes, page merging, existing individual/full-set application, unified manager, collection UI, Go race/vet/module checks and packaged native smoke.
- [x] Verify exact live Avatar set 23427 exposes all 61 images across three rows; confirm no source/process/settings changes to Silo.

Live evidence: `series-artwork-live-verified.json` confirms set 23427 has 61 rendered images in season counts 20/20/21; last image loads in preview, rows scroll horizontally, mobile/desktop have no page overflow, and browser attempted zero writes with zero script errors. Unified manager read-only checks passed on 0.1.17.
