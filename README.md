# Mediux Artwork for Silo

Movie and series posters, backdrops, logos, season posters, and episode title cards from [Mediux](https://mediux.pro). Connects directly to Mediux; no separate container is needed.

## Install

1. Open Silo's administrator **Plugins** page, select **Catalog**, and click **Add repository**. Give it a name such as **Mediux** and use this URL:

   ```text
   https://raw.githubusercontent.com/Pukabyte/silo-plugin-mediux/main/index.json
   ```

2. In **Catalog**, find **Mediux Artwork** under **Other sources**, click **Install**, then enable it. If it does not appear immediately, reload the page and clear any catalog filters.

Silo downloads the correct binary and checks its checksum. Updates are available through the same repository. Existing ZIP installations are not automatically linked to a repository for updates.

Available for Linux amd64, Linux arm64, and macOS arm64. Linux amd64 is runtime-tested.

## Setup

1. Save your **Mediux API token** in the plugin's settings.
2. Add **Mediux Artwork** to the movie, series, season, and episode provider lists you use. Put it first when you want Mediux artwork. Keep your other metadata providers for title information and TMDB/TVDB IDs.
3. Open **Mediux artwork settings** from the plugin menu or administrator sidebar.
4. Choose your creators and artwork preferences, then save.
5. Refresh an item to apply your choices. Saving settings does not replace existing artwork.

Artwork preference changes save immediately without restarting Silo or entering your saved token again.

## Collection covers

Collection artwork is managed entirely inside the plugin. No Silo source
changes or server restart are needed.

1. In the plugin's native configuration, set **Collection connection → Silo API
   key** to a dedicated administrator API key for this Silo instance. Silo
   stores this secret encrypted; the browser never receives it. The plugin
   discovers the host's internal API URL through the SDK.
2. Open **Artwork Manager**, then its **Collection covers** tab.
3. Choose a collection, search for a Mediux franchise, and preview a poster or
   backdrop. A public image URL may also be pasted directly.
4. Select **Apply** to assign the remote image using Silo's existing collection
   API. Artwork remains a remote HTTP(S) URL; the plugin does not download it.

Selection alone changes nothing. Writes require administrator access, a CSRF
check and the collection's captured ETag. A changed collection must be reloaded
before retrying. Collection definitions, membership and schedules are untouched.

The current authenticated Mediux API exposes movie franchise collections.
Genre/category packs on its website are not exposed by that schema. An empty
search therefore does not mean the website has no matching posters. Direct
public artwork URLs can be used without adding another provider to Mediux.

## Creator priority

Search for a Mediux username or add one manually. Use **Up/Down** to arrange creators; the top entry has highest priority.

- **Only use prioritized creators** restricts artwork to your list.
- **Excluded creators** are never selected.

## Matching season posters and title cards

Season posters and title cards have separate consistency settings, so each can come from a different set.

| Setting | Selection |
| --- | --- |
| **Creator priority first; most artwork per creator** | Choose the highest-priority available creator, then their set with the most season posters or title cards. |
| **Complete set first; then creator priority** | Prefer a complete set, then creator order. If none is complete, use creator order, then coverage. This is the default. |
| **Match series poster set** | Use the same set as the automatically selected series poster. |
| **Choose each image separately** | Choose each image independently. |

Coverage compares distinct seasons or episodes in artwork available from Mediux. It does not inspect your local library. A manually chosen series poster cannot anchor later automatic set selection.

For missing artwork, choose whether to fall back to another Mediux set or leave it without a Mediux replacement. Other configured providers may still supply an image.

## Other artwork settings

- **Best image only** supplies automatic choices. On Silo versions supporting full Mediux galleries, the manual picker shows all matching images, while keeping creator exclusions and the **Only use prioritized creators** filter. Older Silo versions may still limit the picker to automatic choices.
- **All matching images** makes every eligible image available for hosts that do not yet distinguish manual picking from automatic refresh.
- **Optimized JPEG** uses smaller images. **Original** preserves source quality. Logos retain transparency.
- **Cache hours** controls how long Mediux lookup results are reused. Default: 24 hours. Set 0 to disable this cache.

Silo controls artwork storage and image caching. Your API token stays on the server and is never included in image URLs. Episode title cards are supplied during metadata refresh; manual image-picker availability depends on your Silo version.

## Support

[Report an issue](https://github.com/Pukabyte/silo-plugin-mediux/issues). For development, see [CONTRIBUTING.md](CONTRIBUTING.md).

[AGPL-3.0-or-later](LICENSE). Embedded Outfit font: [SIL OFL](OFL-Outfit.txt). Independently maintained; not affiliated with Mediux or Silo.


### Set manager

Open **Artwork Manager** from the plugin menu. Search a Silo movie/show title, choose it and preview its Mediux sets. Click artwork for an enlarged preview and use **Apply image** beneath it, or use **Apply set** to replace matching artwork now and save the set for later refreshes, including season posters and episode title cards. Missing artwork in that set receives no Mediux replacement; manual galleries still show other eligible sets. Removing the preference restores normal ranking. Preferences are shared administrator settings and survive plugin updates.

The **Collection covers** tab groups choices by set and supports exact TMDB collection lookup, related franchise discovery from collection members, and a numeric/Mediux set URL. Save a set as a browsing preference, then choose and explicitly apply a poster or backdrop. Saving a preference alone does not replace a cover. Genre/category packs are still not exposed by this API.

This workflow follows AURA's set grouping and saved-selection model, inspected at [AURA commit 7f62755](https://github.com/mediux-team/AURA/tree/7f627558baeb458fd08ae66816203af790b412ad). It uses remote artwork URLs and existing Silo APIs entirely within the plugin. It does not install AURA or run an automatic image download/sync service.


### Immediate replacement (0.1.15)

In **Artwork Manager**, preview artwork by clicking its thumbnail, then use **Apply image** to replace it. **Apply set** replaces matching artwork on the selected title and its existing seasons/episodes, then saves the preferred set. Only existing local targets are changed; missing targets are skipped. Progress and Stop controls let a set operation stop after the current image. A failed or uncertain operation is not automatically retried; completed changes remain applied.

The plugin passes the remote Mediux HTTPS URL to Silo's official image API. Silo caches and publishes the image immediately, retains the remote URL as its source, and applies its normal manual-image lock. Collection covers continue using the existing remote URL assignment workflow. Removing a preferred set does not undo images already applied.


### Artwork Manager (0.1.16)

Open **Artwork Manager** from the plugin menu. **Movies & shows** and **Collection covers** are tabs in the same window, with each view retaining its state when switching. Existing `/sets` and `/collections` links still open the appropriate manager tab.

Click an image to open an enlarged preview; previews never apply artwork. **Apply image** sits beneath each thumbnail and applies only that image. **Apply set** applies the complete matching movie/show set and remembers it, including locally available season/episode targets. The remote Mediux URL remains the source; Silo's existing caching and publication path is unchanged.

Series artwork uses separate horizontal rows by image type and season. Swipe, scroll, or use row arrows to reach every returned image; click to preview and use Apply image beneath it. Large catalogs load automatically in pages.
