# Unified Artwork Manager and explicit image application

**Goal:** One Artwork Manager window contains Movies & shows and Collection covers. Image clicks open an enlarged preview; individual Apply buttons live beneath thumbnails. Apply set applies the complete selected set.

**Architecture:** Keep existing plugin API/persistence/publication paths. Compose the two plugin-owned views in one shell with real tabs, isolated CSS/IDs, a shared theme and one navigation entry. Preserve old plugin URLs as aliases. No iframe, new dependencies, Silo code changes or restart.

- [x] Add enlarged accessible image dialog and separate per-image Apply controls; preserve complete-set application and remote source retention.
- [x] Combine existing media/collection views under Artwork Manager; isolate IDs/styles and preserve each view's state while switching.
- [x] Test preview causes zero writes, modal focus/Escape, button placement, full-set application and both unified tabs; run existing Go/browser checks.
- [x] Package/hot-update plugin and verify real Artwork Manager navigation/preview without changing live artwork; confirm native process/settings unchanged.

**Evidence:** Plugin 0.1.16 hot-update preserved all settings and 72 collections. Read-only live mobile browser found 17 movie sets and 55 collection images; both enlarged previews loaded, tab state persisted, zero writes/errors/overflow. Isolated browser checks verified individual and full-set application. Native handshake/version/checksum smoke passed. Silo process unchanged.
