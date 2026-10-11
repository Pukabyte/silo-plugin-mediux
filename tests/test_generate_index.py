#!/usr/bin/env python3
"""Focused checks for the self-hosted Silo repository index generator."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("generate_index", ROOT / "tools" / "generate_index.py")
assert SPEC and SPEC.loader
GENERATE_INDEX = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(GENERATE_INDEX)


class GenerateIndexTests(unittest.TestCase):
    def test_build_index_has_raw_binaries_and_platform_checksums(self):
        manifest = {
            "plugin_id": "community.mediux",
            "version": "0.1.7",
            "checksum": "__CHECKSUM__",
            "silo_api_version": "v1",
            "supported_platforms": [
                {"os": "linux", "arch": "amd64"},
                {"os": "linux", "arch": "arm64"},
                {"os": "darwin", "arch": "arm64"},
            ],
        }
        digests = {
            "plugin-linux-amd64": "a" * 64,
            "plugin-linux-arm64": "b" * 64,
            "plugin-darwin-arm64": "c" * 64,
        }
        index = GENERATE_INDEX.build_index(
            manifest,
            digests,
            "Pukabyte/silo-plugin-mediux",
            "https://github.com/Pukabyte/silo-plugin-mediux/releases/download/v0.1.7",
        )

        package = index["plugins"][0]
        self.assertNotIn("checksum", package["manifest"])
        self.assertEqual("a" * 64, package["binaries"]["linux/amd64"]["checksum"])
        self.assertEqual(
            "https://github.com/Pukabyte/silo-plugin-mediux/releases/download/v0.1.7/plugin-linux-amd64",
            package["binaries"]["linux/amd64"]["url"],
        )
        self.assertEqual(3, len(package["binaries"]))

    def test_checksum_parser_rejects_missing_or_unsafe_names(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            path = Path(temp_dir) / "checksums.txt"
            path.write_text(("a" * 64) + "  ../plugin-linux-amd64\n", encoding="utf-8")
            with self.assertRaises(ValueError):
                GENERATE_INDEX.parse_checksum_file(path)

    def test_build_index_requires_every_declared_platform(self):
        with self.assertRaisesRegex(ValueError, "missing plugin-linux-arm64"):
            GENERATE_INDEX.build_index(
                {"version": "0.1.7", "supported_platforms": [{"os": "linux", "arch": "arm64"}]},
                {},
                "Pukabyte/silo-plugin-mediux",
                "https://example.invalid/release",
            )

    def test_release_assets_must_match_checksums(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            directory = Path(temp_dir)
            asset = directory / "plugin-linux-amd64"
            asset.write_bytes(b"mediux")
            manifest = {"supported_platforms": [{"os": "linux", "arch": "amd64"}]}
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                GENERATE_INDEX.validate_release_assets(directory, manifest, {"plugin-linux-amd64": "a" * 64})


if __name__ == "__main__":
    unittest.main()
