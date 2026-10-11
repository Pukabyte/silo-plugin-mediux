#!/usr/bin/env python3
"""Generate a Silo plugin repository index for one released Mediux version.

The index lists platform-specific release binaries and their SHA-256 digests.
Silo downloads the matching binary itself; users only add the index URL.
"""

import argparse
import hashlib
import json
from pathlib import Path
import re
import sys
from urllib.parse import quote


ROOT = Path(__file__).resolve().parents[1]
DEFAULT_REPOSITORY = "Pukabyte/silo-plugin-mediux"
SHA256 = re.compile(r"^[0-9a-f]{64}$")


def parse_checksum_file(path: Path) -> dict[str, str]:
    checksums: dict[str, str] = {}
    for line_number, raw_line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw_line.strip()
        if not line:
            continue
        parts = line.split(maxsplit=1)
        if len(parts) != 2 or not SHA256.fullmatch(parts[0].lower()):
            raise ValueError(f"{path}:{line_number}: expected '<sha256>  <filename>'")
        filename = parts[1].lstrip("*").strip()
        if not filename or "/" in filename or "\\" in filename:
            raise ValueError(f"{path}:{line_number}: invalid release asset name {filename!r}")
        if filename in checksums:
            raise ValueError(f"{path}:{line_number}: duplicate checksum for {filename}")
        checksums[filename] = parts[0].lower()
    return checksums


def validate_repository(repository: str) -> str:
    parts = repository.split("/")
    if len(parts) != 2 or any(not part or part in {".", ".."} for part in parts):
        raise ValueError("repository must be in owner/name form")
    if any(any(char.isspace() for char in part) for part in parts):
        raise ValueError("repository must not contain whitespace")
    return repository


def release_url(repository: str, version: str) -> str:
    if not version or "/" in version or "\\" in version:
        raise ValueError("version must be a release version, without path separators")
    return "https://github.com/{}/releases/download/v{}".format(
        repository, quote(version, safe=".-_"))


def validate_release_assets(dist_dir: Path, manifest: dict, checksums: dict[str, str]) -> None:
    """Refuse an index unless each advertised binary matches checksums.txt."""
    platforms = manifest.get("supported_platforms")
    if not isinstance(platforms, list) or not platforms:
        raise ValueError("manifest.json supported_platforms is required")
    for platform in platforms:
        if not isinstance(platform, dict):
            raise ValueError("manifest.json supported_platforms entries must be objects")
        os_name, architecture = platform.get("os"), platform.get("arch")
        if not isinstance(os_name, str) or not os_name or not isinstance(architecture, str) or not architecture:
            raise ValueError("manifest.json supported_platforms entries require os and arch")
        filename = f"plugin-{os_name}-{architecture}"
        expected = checksums.get(filename)
        if expected is None:
            raise ValueError(f"checksums.txt is missing {filename}")
        asset = dist_dir / filename
        if not asset.is_file():
            raise ValueError(f"release asset is missing {asset}")
        actual = hashlib.sha256(asset.read_bytes()).hexdigest()
        if actual != expected:
            raise ValueError(f"checksum mismatch for {filename}")


def build_index(manifest: dict, checksums: dict[str, str], repository: str, base_url: str) -> dict:
    version = manifest.get("version")
    if not isinstance(version, str) or not version:
        raise ValueError("manifest.json version is required")
    platforms = manifest.get("supported_platforms")
    if not isinstance(platforms, list) or not platforms:
        raise ValueError("manifest.json supported_platforms is required")

    binaries: dict[str, dict[str, str]] = {}
    for platform in platforms:
        if not isinstance(platform, dict):
            raise ValueError("manifest.json supported_platforms entries must be objects")
        os_name, architecture = platform.get("os"), platform.get("arch")
        if not isinstance(os_name, str) or not os_name or not isinstance(architecture, str) or not architecture:
            raise ValueError("manifest.json supported_platforms entries require os and arch")
        key = f"{os_name}/{architecture}"
        if key in binaries:
            raise ValueError(f"manifest.json declares duplicate platform {key}")
        filename = f"plugin-{os_name}-{architecture}"
        checksum = checksums.get(filename)
        if checksum is None:
            raise ValueError(f"checksums.txt is missing {filename}")
        binaries[key] = {
            "url": base_url.rstrip("/") + "/" + quote(filename, safe="-._"),
            "checksum": checksum,
        }

    catalog_manifest = dict(manifest)
    # Checksums belong to the platform binary in a catalog. The package manifest
    # carries a build-time placeholder and must not advertise that placeholder.
    catalog_manifest.pop("checksum", None)
    return {
        "plugins": [{
            "manifest": catalog_manifest,
            "repo_url": "https://github.com/" + repository,
            "checksums_url": base_url.rstrip("/") + "/checksums.txt",
            "binaries": binaries,
        }]
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", default=DEFAULT_REPOSITORY, help="GitHub repository in owner/name form")
    parser.add_argument("--version", help="release version; defaults to manifest.json version")
    parser.add_argument("--dist-dir", type=Path, help="directory containing checksums.txt")
    parser.add_argument("--release-url", help="base URL containing plugin-* release assets")
    parser.add_argument("--output", type=Path, default=ROOT / "index.json", help="output index path")
    args = parser.parse_args()

    try:
        repository = validate_repository(args.repo)
        manifest = json.loads((ROOT / "manifest.json").read_text(encoding="utf-8"))
        version = args.version or manifest.get("version")
        if version != manifest.get("version"):
            raise ValueError("--version must match manifest.json version")
        if not isinstance(version, str):
            raise ValueError("manifest.json version is required")
        dist_dir = args.dist_dir or ROOT / "dist" / version
        checksums = parse_checksum_file(dist_dir / "checksums.txt")
        validate_release_assets(dist_dir, manifest, checksums)
        index = build_index(manifest, checksums, repository, args.release_url or release_url(repository, version))
    except (OSError, ValueError, json.JSONDecodeError) as error:
        parser.error(str(error))

    encoded = json.dumps(index, indent=2, ensure_ascii=False) + "\n"
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(encoded, encoding="utf-8")
    return 0


if __name__ == "__main__":
    sys.exit(main())
