# AURA-style set management implementation plan

**Goal:** Browse Mediux collection sets with grouped previews and remember explicit movie, show and collection set choices entirely in the existing plugin.

**Architecture:** Reuse Mediux GraphQL and Silo's existing authenticated plugin routes/config broker. Preserve remote artwork URLs and existing collection ETags. New movie/show preferences narrow automatic artwork to the selected set; galleries remain complete. No Silo source changes, restarts, new dependencies, image downloads or membership writes.

**Reference:** AURA MIT source 7f627558baeb458fd08ae66816203af790b412ad. Its collection artwork queries group posters/backdrops by collection_set, identify franchises via TMDB/member IDs, and offer set previews and saved selections. Genre packs remain unsupported by that API.

## Deliverables

- [x] Extend collection discovery with exact TMDB lookup, bounded member TMDB discovery and numeric/mediux.pro set URL lookup; retain bounded title search and exclude member-movie assets.
- [x] Add grouped set cards/filtering to collection UI and saved preferred set controls. Selection and saving a preference do not apply artwork; Apply remains explicit.
- [x] Add plugin set manager for movie/show TMDB IDs, previews/counts and save/remove set preference, with collection manager navigation.
- [x] Persist bounded bindings in a separate encrypted set_preferences entry. Validate admin, CSRF, target and membership; persistence failures leave active bindings unchanged.
- [x] Apply saved bindings to automatic movie/show/season/episode artwork only. Galleries show all sets. Missing selected-set artwork leaves the image unchanged.
- [x] Add targeted Go and browser regression tests, run race/vet/format/module/Python/browser checks, package and hot-update plugin with rollback artifact.
- [x] Verify real collection discovery/set lookup, temporary hidden collection remote assignment and saved preference removal; confirm original collections/config and unchanged Silo process/binary.

## API contracts

GET /sets/catalog?type=movie|series&id=<TMDB>: {type,id,sets:[{id,title,creator,popularity,artwork:[{id,kind,url,set_id,season,episode,language}]}],preferred_set_id}.
GET /sets/preferences: {bindings:{"movie:123":"456",...},csrf}.
PUT /sets/preferences: {csrf,type:movie|series|collection,id,set_id}; empty set_id removes a preference. Collection id is the local Silo collection ID. Returns {bindings,csrf}.
GET /collection-artwork?q=<title>|tmdb=<collectionTMDB>|movie=<memberTMDB>|set=<numericID-or-mediux.pro/sets/ID>: existing {items,has_more}, enriched artwork has set_title and collection_id. Explicit selectors are mutually exclusive.

All routes remain admin-only. The standalone pages retain current theme/readability and mobile layout. AURA is reference behavior; no wholesale application code copy.


## Verification evidence

- AURA source pinned at 7f627558baeb458fd08ae66816203af790b412ad; live Go GraphQL probes returned HTTP 200 with zero GraphQL errors for exact collection, member movie, direct set and movie artwork queries.
- Go race tests, vet, formatting, module verification, four Python checks, isolated collection/set browser suites and native runtime handshake passed.
- Live browser verified 55 collection choices, direct/exact/member lookup, hidden manual membership discovery, native 2002 Spider-Man title selection (17 sets), collection/movie preference lifecycle, remote poster persistence and image rendering; zero page errors.
- Temporary hidden collection and all test preferences removed. Final readback: plugin 0.1.14, both manager routes navigable, 72 original collections, 34 Mediux gallery images, preserved artwork settings digest and health ok.
- Silo source worktree clean; Silo PID 972084 and binary hash unchanged. Rollback archive retained locally as plugin-aura-rollback-0.1.13.zip.
