# Development and maintenance

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


## Community catalog integration

Approval follows [Silo Community's policy](https://github.com/Silo-Community/silo-plugins/blob/main/APPROVAL_POLICY.md). Organization ownership and independent maintainer acceptance are prerequisites; this personal repository does not claim community approval. `CODEOWNERS` retains @Pukabyte as maintainer.

After transfer and approval, configure `SILO_PLUGINS_DISPATCH_TOKEN` as a fine-grained token limited to `Silo-Community/silo-plugins`, with the minimum permission required for repository dispatch. Tagged releases then dispatch `plugin_release_published` to `Silo-Community/silo-plugins`. Until configured, maintainers can run that catalog's **Update Catalog** workflow manually with the repository and release tag. Release publishing and the personal install index do not require this secret. Organization branch protection must permit the release bot to update only `index.json`, or maintainers must commit the released index through their required PR process. The released `index.json` asset remains available if the main-branch update is blocked.

## Validation limits

Linux amd64 is runtime-tested. Linux arm64 and macOS arm64 are cross-built. Cache-enabled end-to-end delivery remains unverified. External-ID image lookup requires Silo's `lookup_images` support (PR #3145, commit `1556ddb`); older hosts must forward the Mediux identity. Native picker requests without the optional custom request-purpose header use the configured selection policy, so BestOnly can show one image per kind. The plugin-owned Artwork Manager does not require this header. The episode gallery request does not provide an episode number.

See [DESIGN.md](DESIGN.md) for API, cache, resolver and settings implementation details.
