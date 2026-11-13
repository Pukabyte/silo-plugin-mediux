# Implementation notes

## Silo contracts

The plugin uses the tagged Silo SDK v0.24.0 and exposes `metadata_provider.v1`, `image_resolver.v1`, and administrator-only `http_routes.v1` capabilities. Silo's native runtime supplies the authenticated user role and persists configuration through its host broker.

TMDB IDs identify Mediux movies and shows. TVDB-only identities are mapped through Mediux's `movies` or `shows` GraphQL filter, then normal lookup bootstraps a `mediux` identity such as `series:82452`. External IDs are declared through `lookup_provider_ids`; `image_lookup_provider_ids` also declares the adapter support needed for galleries. Artwork enrichment does not fabricate textual titles or episode names.

The executable is a plugin runtime, not a standalone HTTP server or library scanner. Silo controls provider chains, refresh/merge rules, image locks, storage, and image delivery.

## Mediux API

Metadata requests use `https://images.mediux.io/graphql` with an `Authorization: Bearer` header. Asset URLs use `https://images.mediux.io/assets/`. Creator search queries public `contributors` usernames with an `icontains` filter and a bounded result count. No configurable endpoint or token-bearing image URL is accepted.

The API contract was tested with authenticated requests and isolated native SDK calls. Mediux's GraphQL schema is a dependency that may change. This plugin does not claim an official provider compatibility guarantee or depend on the Mediux container's private routes.

Movie requests retrieve standalone sets and movie-specific collection posters
and backdrops in one GraphQL call. Collection assets retain their set, creator,
popularity, language and modification version, then share the existing selection
and cache paths. Assets must identify the requested movie; collection covers and
other movies' artwork are excluded.

The administrator-only `GET /collection-artwork?q=...` route separately searches
Mediux movie collections and returns their own posters and backdrops. It uses
the saved credential, the same fixed GraphQL/asset hosts, concurrency limit and
creator preferences. Search input, response bytes, collections, sets, assets
and returned candidates are bounded. Movie-specific images are excluded.
Results include only stable, credential-free HTTPS image URLs for selection
and preview. Collection artwork uses its existing remote HTTPS viewer; movie
plugin references are not advertised as collection assignment values.
The plugin-owned collection manager assigns selected HTTP(S) URLs through
Silo's existing `poster_url` and `backdrop_url` API fields. No Silo source
changes are needed. This does not change automatic movie artwork.

The authenticated schema verified for this release exposes franchise
collections, not genre/category sets. An empty genre search is not evidence
that the Mediux website has no matching artwork. Website-only packs are not
scraped or silently substituted; external public image URLs can be selected
through Silo's remote artwork field instead.

API responses are bounded, requests have cancellation/timeouts, redirects are rejected, and concurrency is limited. The metadata cache holds at most 128 bundles, coalesces duplicate in-flight requests, expires empty results sooner, and uses configuration generations so older requests cannot repopulate a new configuration's cache. Configuration changes invalidate the cache. Cache bounds count entries, not decoded bytes; large titles can consume substantial memory.

GraphQL failures include the operation, upstream message, optional error code
and field path in the returned RPC error. Only these fields are read; arbitrary
extensions and upstream stacks are omitted. Diagnostics redact the configured
token (including URL-encoded forms) and bearer credentials before limiting each
detail to 512 characters and reporting at most three errors. Control characters
are flattened so upstream content cannot inject additional log lines.

## Artwork references

Canonical references have the form `mediux://<asset-id>?v=<modified-version>&quality=jpg`. The resolver validates asset UUIDs and restricts delivery to the fixed Mediux asset host. Single and batch resolver RPCs share the same behavior. Quality determines optimized or original delivery; logos retain original transparency. No metadata API token is exposed in references, HTML, creator results, or settings responses. Metadata ingestion assumes Mediux asset IDs are valid; malformed upstream IDs can produce unusable references, which the resolver rejects. Missing or invalid modification timestamps use the stable `v=0` fallback.

Sampled valid assets were publicly retrievable using browser and Silo downloader clients. Some generic clients or inaccessible IDs can receive HTTP 403. If Mediux starts requiring authentication for valid assets, credential-free direct resolution will need a new provider-owned delivery mechanism. The current code does not offer a credential relay.

Silo decides whether to resolve references directly or download/cache image variants. Direct delivery is tested. Cache-enabled end-to-end acceptance remains outstanding.

## Selection and coverage

Creator matching is case-insensitive. Exclusions and prioritized-only filtering apply before artwork candidates are ranked. Automatic series-poster selection follows language tiers; backdrops/logos prefer that series set. Season posters and title cards independently compute their selected set once per response.

Sets are ranked by Mediux `popularity_global`, a rank among all Mediux sets, not a score: 1 is the best set. Unlike `popularity`, which ranks a set only among the sets of one title, the global rank puts movie, collection and show sets on one scale, so a creator's movie set and collection set compare correctly. Within the same creator rank, sets sort by ascending rank, and unranked sets (0 or null) sort after every ranked set. This matches the ordering in Mediux's own AURA client.

`CreatorPriority` ranks creator order before unique scope coverage. `MostComplete` prefers complete candidates, then creator order; incomplete candidates rank by creator order before coverage. Duplicate artwork does not improve a set's coverage. Language, popularity and set ID break coverage ties. Title-card variants preserve creator priority, then prefer the requested language, English, neutral artwork, and other languages; UUID order does not override that language preference. Gap handling returns another eligible set's artwork or no Mediux replacement.

Completeness is measured against the union of artwork scopes returned by Mediux. The SDK does not expose Silo's local child inventory here; future-season artwork can affect coverage. Explicit per-title set pinning is available in the plugin set manager. Episode-gallery lookup remains outside the current contract.

## Private settings

The secret token remains in the native `settings` entry. Public artwork preferences persist through `runtime.Host().SetGlobalConfigEntry` in a separate encrypted `artwork_preferences` entry. Configure merges these preferences over legacy values without overwriting the token. Preferences apply immediately and survive a plugin restart.

Routes require the Silo-supplied administrator role. JSON preference writes require a cryptographically random per-process CSRF token, use a strict field allowlist and payload limit, validate choices/creator lists, and serialize saves. A persistence failure leaves active preferences unchanged. The plugin never returns an API token; the page exposes only whether one is configured.

The standalone settings page uses Silo's existing administrator launch flow. Its embedded licensed Outfit font avoids external font fetches. Public theme variables are read without credentials and validated against an allowlist; arbitrary `raw_css` is ignored. Cached readability preferences use Silo's existing owner namespace. Theme failure retains bundled Cinema Dark defaults.

## Validation and release boundaries

Go tests cover native configuration, identity lookup, API decoding, concurrency/cache behavior, artwork selection, references/resolution, creator lookup, administrator access and preference persistence. Browser checks cover search/add/reorder/remove, repeated saves, errors, theme overrides, readability preferences, keyboard focus and mobile overflow.

Releases contain checksum-bearing manifests and binaries for all declared platforms. Linux amd64 has native runtime acceptance; Linux arm64 and macOS arm64 are cross-builds. Production deployment tools, configuration, backups and operational evidence are excluded from source control. A GitHub publication does not register or approve the plugin in Silo's community catalog.

## Plugin-owned collection management

The collection manager is an administrator-only plugin page. Its backend uses
an optional `collection_connection` encrypted global configuration entry and
SDK `GetHostInfo.InternalBaseURL` to call only Silo's existing collection
endpoints. The dedicated Silo API key is never placed in HTML, JSON, browser
storage or image URLs. The browser sends only the plugin CSRF token and a
captured collection ETag. Native request authentication is not forwarded from
the browser and no session or refresh credential is scraped.

Listing, preview and selection are read-only. Explicit Apply submits exactly
one artwork field through native PATCH/If-Match; native precondition failures
are not retried. The result is read back from the viewer artwork listing,
because the canonical native editor body intentionally omits expiring artwork
URLs. Collection rules, membership, library scope and scheduling are not
changed. Host errors and credentials are never relayed verbatim. Requests and
response sizes are bounded, and redirects are rejected.


## AURA-style set selection (0.1.14)

Reference behavior was inspected at AURA MIT commit `7f627558baeb458fd08ae66816203af790b412ad`. AURA groups collection poster/backdrop assets by `collection_set`, follows movie TMDB collection relationships, and saves per-title set selections. The plugin implements these workflows with its own routes and the existing Silo runtime; it does not embed or run AURA.

Collection discovery accepts a title, exact collection TMDB ID, member movie TMDB ID, or numeric/Mediux collection-set URL. The native discovery action uses the collection's source TMDB identity first; other collections use up to four members from a bounded eight-row page. These are related franchises, not genre matches, and the UI explains the distinction. Only collection-owned artwork is returned; member movie artwork is excluded. There are no membership mutations.

The set manager searches native movie/show titles through existing Silo catalog endpoints. Selecting a title resolves its TMDB (or TVDB-to-TMDB) identity, groups existing Mediux artwork by set, and previews poster, backdrop, season, titlecard and logo assets. Saved preferences use a separate encrypted `set_preferences` global entry keyed by movie/show TMDB ID or local collection ID, bounded to 500 targets. They are shared administrator preferences, not personal user-profile settings.

Movie/show bindings narrow automatic provider responses to that set, including later season/episode refreshes. Manual image galleries retain other eligible sets. Missing or excluded selected-set artwork returns no Mediux replacement. Saving does not trigger a refresh, download images or overwrite existing images immediately; Silo's provider chains, image locks and merge behavior remain authoritative. Collection bindings remember a browsing preference only; cover changes still use explicit single-slot Apply and native ETags. Removal restores normal automatic ranking.

All set routes require administrator access. Writes use strict bounded JSON and constant-time CSRF verification before external reads. Set availability is verified against the target's eligible artwork before persistence, and a failed persistence call leaves active preferences unchanged. Keys and Mediux credentials never reach HTML, JSON or image URLs. No new dependencies, Silo source changes or Silo restart are required.


Native catalog search, detail and member reads require Silo's documented `X-Profile-Id`. The plugin obtains profiles through the dedicated API key account's `GET /api/v2/profiles`, selects only its primary adult profile without a PIN, and sends that identifier server-side. It never forwards browser profile credentials or PINs. If no eligible profile exists, native browsing returns a configuration explanation; direct TMDB set lookup remains available. Live read checks verified these routes with the official profile header before deployment.

Manual collection discovery reads memberships through the existing admin endpoint, including hidden collections. Query collections use the profile-scoped library member view and retain its access/visibility rules. Neither path modifies membership.


## Immediate image application (0.1.15)

`POST /sets/plan` validates the native parent identity against the requested Mediux movie/show TMDB ID and resolves existing child content IDs from official catalog APIs. It reads complete sets, respects creator eligibility, picks one variant per target slot, and returns at most 1000 operations. Individual choices identify the exact asset and scope. No artwork changes during planning.

Each operation binds a random nonce, its target, fixed Mediux HTTPS source, image kind, title/set identity, administrator role and fifteen-minute expiry in a server-generated HMAC token. The signing key is random, process-local and separate from the publicly returned CSRF token. `POST /sets/apply` requires administrator access and CSRF, verifies and atomically consumes the signed operation, then invokes the existing Silo image apply API exactly once. The browser executes operations sequentially and reports partial/unknown results without retries. Set preference persistence follows completed whole-set application; a preference save failure is reported separately from already-applied artwork.

Movie/show publication uses Silo's official synchronous caching/publication path. Source fields retain the exact remote Mediux HTTPS URL; display can use cached immutable artwork. Native publication sets the image lock and emits catalog change events. This feature adds no Silo source changes, local upload flow, custom image store or background mutation job. Collections retain their ETag-protected remote-reference behavior.

Consumed operation digests remain used even after native errors or timeouts. A bounded process-local registry prevents duplicate publications; a new explicit selection creates a fresh nonce. Restart rotates the signing key and invalidates old operations.


## Unified manager and explicit application (0.1.16)

The navigable `/artwork` route composes the two embedded views into one **Artwork Manager**, with state-preserving Movies & shows and Collection covers tabs. Legacy GET `/sets` and `/collections` routes are aliases; their data APIs are unchanged. The renderer namespaces static IDs, ARIA references, component lookups and CSS to isolate view state without frames or dynamic evaluation. The shell owns the common theme, navigation and keyboard tab controls.

Thumbnail clicks open an enlarged native dialog with Escape/Close and focus restoration. Application uses separate buttons beneath each thumbnail. Movie/show **Apply set** still plans and applies the complete selected set, independent of the bounded preview gallery. Collection thumbnails also have explicit individual Apply controls and retain their existing remote assignment/native ETag checks. Neither preview flow writes artwork or preferences.

Series galleries separate artwork types and seasons into named horizontal regions, order episodes numerically, and offer native touch/keyboard scrolling plus arrow buttons. Catalog pages retain all image metadata without the former eight-per-type preview cap.
