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

API responses are bounded, requests have cancellation/timeouts, redirects are rejected, and concurrency is limited. The metadata cache holds at most 128 bundles, coalesces duplicate in-flight requests, expires empty results sooner, and uses configuration generations so older requests cannot repopulate a new configuration's cache. Configuration changes invalidate the cache. Cache bounds count entries, not decoded bytes; large titles can consume substantial memory.

## Artwork references

Canonical references have the form `mediux://<asset-id>?v=<modified-version>&quality=jpg`. The resolver validates asset UUIDs and restricts delivery to the fixed Mediux asset host. Single and batch resolver RPCs share the same behavior. Quality determines optimized or original delivery; logos retain original transparency. No metadata API token is exposed in references, HTML, creator results, or settings responses. Metadata ingestion assumes Mediux asset IDs are valid; malformed upstream IDs can produce unusable references, which the resolver rejects. Missing or invalid modification timestamps use the stable `v=0` fallback.

Sampled valid assets were publicly retrievable using browser and Silo downloader clients. Some generic clients or inaccessible IDs can receive HTTP 403. If Mediux starts requiring authentication for valid assets, credential-free direct resolution will need a new provider-owned delivery mechanism. The current code does not offer a credential relay.

Silo decides whether to resolve references directly or download/cache image variants. Direct delivery is tested. Cache-enabled end-to-end acceptance remains outstanding.

## Selection and coverage

Creator matching is case-insensitive. Exclusions and prioritized-only filtering apply before artwork candidates are ranked. Automatic series-poster selection follows language tiers; backdrops/logos prefer that series set. Season posters and title cards independently compute their selected set once per response.

`CreatorPriority` ranks creator order before unique scope coverage. `MostComplete` prefers complete candidates, then creator order; incomplete candidates rank by creator order before coverage. Duplicate artwork does not improve a set's coverage. Language, popularity and set ID break coverage ties. Title-card variants preserve creator priority, then prefer the requested language, English, neutral artwork, and other languages; UUID order does not override that language preference. Gap handling returns another eligible set's artwork or no Mediux replacement.

Completeness is measured against the union of artwork scopes returned by Mediux. The SDK does not expose Silo's local child inventory here; future-season artwork can affect coverage. Explicit set pinning, anchoring to a manually chosen poster and episode-gallery lookup are outside the current contract.

## Private settings

The secret token remains in the native `settings` entry. Public artwork preferences persist through `runtime.Host().SetGlobalConfigEntry` in a separate encrypted `artwork_preferences` entry. Configure merges these preferences over legacy values without overwriting the token. Preferences apply immediately and survive a plugin restart.

Routes require the Silo-supplied administrator role. JSON preference writes require a cryptographically random per-process CSRF token, use a strict field allowlist and payload limit, validate choices/creator lists, and serialize saves. A persistence failure leaves active preferences unchanged. The plugin never returns an API token; the page exposes only whether one is configured.

The standalone settings page uses Silo's existing administrator launch flow. Its embedded licensed Outfit font avoids external font fetches. Public theme variables are read without credentials and validated against an allowlist; arbitrary `raw_css` is ignored. Cached readability preferences use Silo's existing owner namespace. Theme failure retains bundled Cinema Dark defaults.

## Validation and release boundaries

Go tests cover native configuration, identity lookup, API decoding, concurrency/cache behavior, artwork selection, references/resolution, creator lookup, administrator access and preference persistence. Browser checks cover search/add/reorder/remove, repeated saves, errors, theme overrides, readability preferences, keyboard focus and mobile overflow.

Releases contain checksum-bearing manifests and binaries for all declared platforms. Linux amd64 has native runtime acceptance; Linux arm64 and macOS arm64 are cross-builds. Production deployment tools, configuration, backups and operational evidence are excluded from source control. A GitHub publication does not register or approve the plugin in Silo's community catalog.
