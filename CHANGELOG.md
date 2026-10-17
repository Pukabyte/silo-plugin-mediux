# Changelog

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
