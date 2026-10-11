# Mediux artwork for Silo

A native Silo plugin for movie and series posters, backdrops, logos, season posters, and episode title cards from [Mediux](https://mediux.pro). It uses existing TMDB/TVDB identities and leaves text metadata to your other providers.

Metadata requests go directly to Mediux's GraphQL API with your saved token. Image references contain no credentials. Silo resolves them through its normal artwork pipeline; a separate Mediux container is not required.

## Install

In Silo's administrator plugin page, add this repository URL:

```text
https://raw.githubusercontent.com/Pukabyte/silo-plugin-mediux/main/index.json
```

Refresh the repository, install **Mediux Artwork**, then enable it. Silo downloads the matching platform binary and verifies its SHA-256 checksum. The source `manifest.json` describes the plugin; it is not an install repository index.

This custom repository supports normal installation and update discovery while community approval is pending. ZIP uploads remain available for offline installations. Existing ZIP installs are not automatically linked to this repository for updates; repository update tracking applies to installations made through the repository.

1. Save your Mediux API token in the plugin's standard settings form.
2. Add Mediux to the applicable movie, series, season, and episode provider chains. Put it early when it should supply artwork. Keep other providers for text and IDs.
3. Open **Mediux artwork settings** from the plugin menu or administrator sidebar. Set creator order and artwork policies, then save.
4. Refresh an item to apply new artwork choices. Saving preferences alone does not replace existing artwork.

Requires Silo's native plugin runtime with administrator plugin pages and SDK v0.24.0 contracts. Linux amd64 is runtime-tested. Linux arm64 and macOS arm64 are cross-built; runtime acceptance on those platforms remains outstanding.

This is an independently maintained community plugin. Publication does not mean approval for Silo's community catalog.

## Creator order and set consistency

Search Mediux usernames, add them manually, reorder with Up/Down, or remove entries. Creators at the top have higher priority. Excluded creators are ignored; **Only use prioritized creators** limits selection to your list.

Season posters and title cards choose their sets independently:

| Policy | Selection |
| --- | --- |
| **Creator priority first; most artwork per creator** | Highest-priority available creator, then their set covering the most distinct seasons or episodes. |
| **Complete set first; then creator priority** | Prefer sets covering every scope represented in Mediux artwork, then creator order. If no set is complete, creator order precedes coverage. This is the default. |
| **Match series poster set** | Follow the automatically selected series poster's set. |
| **Choose each image separately** | Rank each image independently. |

Coverage counts distinct seasons or episodes, not duplicate image versions. Language, popularity, then set ID break coverage ties. Title-card coverage is calculated across the show. Coverage uses Mediux's returned artwork, not the seasons or episodes present in your Silo library; artwork for future seasons can influence it.

Missing artwork can fall back to another Mediux set or return no Mediux replacement. **Keep existing; no other Mediux set** only controls this plugin; other configured providers and Silo's refresh rules may still replace an image.

The series poster follows Silo's language preference tiers, then creator/popularity order. Title cards preserve creator priority, then prefer the requested language, English, neutral artwork, and other languages within that priority tier. Backdrops and logos prefer its set. A poster chosen manually in Silo cannot anchor later refreshes because the SDK does not pass that selection back to the plugin.

## Settings and image delivery

Creator/artwork preferences save immediately without a Silo restart or resending the saved API token. They persist separately in Silo's encrypted plugin configuration. The browser receives only preferences, creator usernames, token presence, and a CSRF token.

The settings page matches Silo's theme colours, Outfit typography, controls, and cached text-scale, weight, and contrast preferences. Arbitrary custom theme CSS is not imported. If sign-in expires, return to Silo and reopen the page.

- **Best image only** returns automatic artwork choices. **All matching images** offers more gallery candidates while singular metadata records retain the configured consistency policy.
- **Optimized JPEG** reduces image size. **Original** preserves source quality. Logos always use original images for transparency.
- **Cache hours** caches API metadata, not image bytes. Default: 24 hours; 0 disables reuse; maximum: 168 hours. Preference changes clear this cache.
- Silo controls image storage with `metadata.cache_images` and its artwork backend. The plugin neither changes those settings nor maintains a separate image cache.

Valid sampled Mediux assets were anonymously accessible to browser and Silo downloader requests. Public access is a provider behavior, not a guarantee. Tokens are never placed in image URLs.

## Current limits

- Normal metadata refresh works independently of the manual image picker. Mediux's gallery visibility requires Silo's metadata adapter to honour `image_lookup_provider_ids`, or to forward an existing Mediux provider identity. A metadata refresh bootstraps that identity, but older picker implementations may still omit it from gallery requests.
- The current image-gallery request has no episode number. Title cards are supplied through episode metadata rather than an episode gallery.
- Explicit set pinning and coverage based on your local library inventory are not implemented.
- Large API responses are bounded to 2 MiB and upstream requests have timeouts. Oversized responses fail rather than being silently truncated. The metadata cache is bounded to 128 entries, rather than a total byte budget; large artwork bundles can use substantial memory.
- Automatic image delivery is runtime-tested with Silo caching disabled. Cache-enabled end-to-end delivery remains unverified.

See [DESIGN.md](DESIGN.md) for implementation details.

## Development

Use the Go version declared in `go.mod`. No local SDK replacement is required.

```sh
GOWORK=off make check audit build
python3 tools/package.py
go run ./tools/smoke "$(pwd)/dist/0.1.7/plugin-linux-amd64" 0.1.7
```

CI also checks that two Linux archives are byte-identical. Packaging builds all declared platforms, supplies checksum-bearing manifests, and writes installable ZIPs, raw binaries, and `checksums.txt` under `dist/<version>/`.

The existing browser checks use Playwright. With Playwright and Chromium available in your development environment:

```sh
node tests/settings-ui.test.cjs
```

Optional `MEDIUX_SCREENSHOT_DIR` selects a screenshot directory. `MEDIUX_BEFORE_HTML` enables before/after comparisons. Browser fixtures are isolated; these tests do not contact Silo or Mediux.

## License

[AGPL-3.0-or-later](LICENSE). Embedded Outfit is distributed under the [SIL Open Font License](OFL-Outfit.txt). This plugin is not affiliated with Mediux or the Silo project.

## Community catalog integration

Approval follows [Silo Community's policy](https://github.com/Silo-Community/silo-plugins/blob/main/APPROVAL_POLICY.md). Organization ownership and independent maintainer acceptance are prerequisites; this personal repository does not claim community approval. `CODEOWNERS` retains @Pukabyte as maintainer.

After transfer and approval, configure `SILO_PLUGINS_DISPATCH_TOKEN` as a fine-grained token limited to `Silo-Community/silo-plugins`, with the minimum permission required for repository dispatch. Tagged releases then dispatch `plugin_release_published` to `Silo-Community/silo-plugins`. Until configured, maintainers can run that catalog's **Update Catalog** workflow manually with the repository and release tag. Release publishing and the personal install index do not require this secret. Organization branch protection must permit the release bot to update only `index.json`, or maintainers must commit the released index through their required PR process. The released `index.json` asset remains available if the main-branch update is blocked.
