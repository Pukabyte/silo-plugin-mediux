# Security policy

Use GitHub's private vulnerability reporting on the repository Security tab to report vulnerabilities. Do not include API tokens or private Silo configuration in public issues.

The latest release is supported. Version 0.1.6 was built with dependencies and a Go toolchain affected by published advisories; upgrade to 0.1.7 or later when available.

CI runs the pinned `govulncheck` tool against the published modules before release. The plugin contacts `images.mediux.io` for authenticated GraphQL metadata and public artwork assets. Credentials remain in Silo's encrypted plugin configuration and are never placed in image URLs.
