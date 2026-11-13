# Immediate artwork selection implementation plan

**Goal:** A movie/show artwork choice in the plugin immediately replaces the matching Silo artwork; whole-set selection applies matching existing items now and remembers the set.

**Architecture:** Plugin-only read-only planning resolves native title/season/episode IDs and validates artwork against a complete Mediux set. Short-lived, single-use HMAC-signed operation tokens authorize individual native image applications. Browser applies sequentially with visible progress and stops on the first failed/uncertain result, without retries. There are no persistent jobs or Silo source changes.

**Native contract:** POST /api/v2/admin/items/{target}/images/apply accepts a provider or HTTP(S) image, caches it and publishes synchronously with a revision. The source confirms remote HTTP(S) is accepted directly by cacheProviderItemImage. There is no supported remote-only write for movie/show artwork. Native publication sets image locks; collections retain existing remote-URL handling.

## API

POST /sets/plan: {csrf,type:movie|series,id:<TMDB>,content_id?:<native parent>,set_id,asset_id?:<UUID>,kind?:poster|backdrop|logo|season|still,season?:number,episode?:number}. Missing asset_id plans the whole set. Returns {type,id,content_id,set_id,operations:[{token,target_id,kind,url,label}],skipped:<count>}. Plans are read-only, max 1000 operations, include only local matching season/episode IDs, choose one variant per slot, respect creator eligibility and never fabricate child IDs.

POST /sets/apply: {csrf,token}. Validates admin/CSRF/signature/expiry, atomically consumes the operation token, then calls native image apply exactly once with a fixed Mediux HTTPS source and provider_id mediux. Returns {target_id,kind,image_url,revision,stored_path}. Unknown completion is reported without retry. Keys stay server-side.

## Tasks

- [x] Add backend plan/apply implementation and security/target/partial-result tests.
- [x] Add immediate individual image actions and Apply & remember set workflow, sequential progress, stop control, stale-target and uncertain-write handling.
- [x] Wire routes, add version/docs and run Go/browser/build verification.
- [x] Hot-update plugin and verify live publication against isolated reversible test media, preserving real artwork and the Silo process.


## Verification

- Go race tests, vet, formatting, module verification, four Python checks, native runtime handshake/checksum and collection/set browser suites passed.
- Browser checks cover immediate single image, three-operation set application, partial native errors, uncertain network completion without retry, preference-save failures, stopping after the current image and stale title responses.
- Signed operation tests cover wrong identities/CSRF, forged/expired/non-canonical tokens, exact/concurrent replays and fresh nonces for new explicit selections.
- Plugin 0.1.15 hot-deployed. Real native publication verified on an isolated metadata-only movie fixture: cached WebP path published, exact remote Mediux HTTPS source retained, native image lock set. Whole selected set and preference persistence verified; test preferences restored.
- Temporary fixture deleted; real movie artwork digest unchanged. Final live readback retained 72 collections, 34 Mediux gallery images, encrypted artwork settings, original Silo PID/binary and health ok. No Silo source changes or restart.
