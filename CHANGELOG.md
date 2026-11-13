# Changelog

## 0.1.17

- Show all returned set images through paged catalog requests instead of eight per type.
- Separate posters, backdrops, logos, season posters and title cards into horizontal scroll rows.
- Group season artwork by season and sort title cards by episode; add accessible scroll controls.

## 0.1.16

- Combine movie/show sets and collection covers in one Artwork Manager window with state-preserving tabs. Keep previous plugin URLs working.
- Clicking artwork opens an enlarged preview; explicit Apply image controls sit beneath individual thumbnails. Apply set applies the complete matching set.
- Preserve existing native caching/publication and remote-source URL retention.

## 0.1.15

- Apply a chosen movie/show image immediately through Silo native image publication, using the remote Mediux HTTPS URL as the retained source.
- Apply and remember a whole set across matching local title, season and episode artwork, with sequential progress, stop control and no automatic retries after failures.
- Validate read-only plans, target identity, creator eligibility and signed short-lived operations inside the plugin.

## 0.1.14

- Add AURA-style collection set groups, direct set URL lookup and exact/member TMDB franchise discovery.
- Add a plugin-owned movie/show set manager with native title search, previews and persistent preferred sets for later automatic artwork refreshes.
- Save collection browsing preferences separately from explicit ETag-safe remote cover assignment. Keep image galleries complete and leave missing selected-set artwork unchanged.

## 0.1.13

- Fix mobile search alignment, stacked action buttons and doubled card spacing, including larger text settings.
- Bound artwork gallery scrolling and explain the connected API franchise-only search coverage before searching.

## 0.1.12

- Add a plugin-owned collection cover manager with browsing, previews and explicit remote URL assignment through existing Silo APIs. No Silo source changes or server restart are required.
- Keep the dedicated Silo connection key in encrypted plugin configuration; administrator access, CSRF and native ETags protect assignments.

## 0.1.11

- Add administrator-only discovery of movie collection cover posters and backdrops. Results include stable remote HTTPS URLs, respect creator preferences, and exclude member-movie artwork. Silo owns selection and assignment.
- Genre/category packs on the Mediux website are not exposed by the verified authenticated API schema; this release does not scrape them or substitute movie artwork.
## Unreleased

- Rank sets by Mediux `popularity_global`, where 1 is best. Automatic selection and season and title-card consistency previously treated the rank as a score and preferred the lowest-ranked set from the same creator, and manual galleries listed it first. The global rank also lets movie and collection sets compare on one scale. Unranked sets still sort last.

## 0.1.10

- Report Mediux GraphQL error messages, error codes and field paths for artwork requests and TVDB identity lookups, including which operation failed. Diagnostics redact the configured token and bearer credentials, omit upstream stacks, and limit output size.

## 0.1.9

- Include movie posters and backdrops from collection sets in creator-priority selection and manual galleries, using the same cached movie request.

## 0.1.8

- Show all eligible Mediux artwork in Silo's manual image picker while retaining automatic best-image selection for refreshes.

## 0.1.7

- Add a Silo repository index for normal plugin installation and update discovery without manual ZIP downloads.
- Upgrade Go to 1.26.9, `golang.org/x/net` to 0.60.0, and gRPC to 1.83.2 to resolve published security advisories. The Silo SDK remains v0.24.0.
- Add vulnerability auditing and reproducible archive checks to CI; update pinned GitHub Actions.
- Add the standard community catalog release dispatch, gated on organization ownership and approval.


## 0.1.6

Initial public release. Includes native Mediux metadata enrichment and image resolution, ordered creator search/preferences, independent season and title-card consistency policies, configurable gap handling, and Silo-themed administrator settings.

- Fix title-card language selection within the selected set. Creator priority remains ahead of language during fallback and independent selection.
- Preserve supported regional language codes such as `pt-BR` and `zh-CN` during language matching.
- Add portable browser checks, reproducible release packaging and GitHub CI.
- Document manual-gallery compatibility, Mediux-based coverage and remaining platform/cache validation limits.

## Earlier local versions

0.1.5 introduced Silo-themed settings. 0.1.4 introduced explicit creator-first consistency. 0.1.3 moved creator and artwork preferences to a plugin-owned page with token-preserving saves. These versions were local deployments, not public GitHub releases.
