#!/usr/bin/env python3
"""Build Silo install archives and checksum-bearing release assets."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import zipfile

ROOT = Path(__file__).resolve().parents[1]


def archive_entry(archive, name, data, mode):
    entry = zipfile.ZipInfo(name, date_time=(2026, 1, 1, 0, 0, 0))
    entry.create_system = 3
    entry.external_attr = mode << 16
    entry.compress_type = zipfile.ZIP_DEFLATED
    archive.writestr(entry, data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version', help='Must equal manifest.json version')
    parser.add_argument('--platform', action='append', help='Build only this os/arch')
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    manifest = json.loads((ROOT / 'manifest.json').read_text())
    version = manifest['version']
    if args.version and args.version != version:
        parser.error('Release version must match manifest.json')
    declared = [f"{p['os']}/{p['arch']}" for p in manifest['supported_platforms']]
    platforms = list(dict.fromkeys(args.platform or declared))
    if any(p not in declared for p in platforms):
        parser.error('Platform must be declared in manifest.json')
    output = (args.output or ROOT / 'dist' / version).resolve()
    output.mkdir(parents=True, exist_ok=True)
    checksums = []
    for platform in platforms:
        goos, goarch = platform.split('/')
        binary = output / f'plugin-{goos}-{goarch}'
        environment = {**os.environ, 'GOOS': goos, 'GOARCH': goarch,
                       'CGO_ENABLED': '0', 'GOWORK': 'off'}
        subprocess.run([os.environ.get('GO', 'go'), 'build', '-buildvcs=false',
                        '-trimpath', '-ldflags', f'-s -w -X main.version={version}',
                        '-o', str(binary), '.'], cwd=ROOT, env=environment, check=True)
        data = binary.read_bytes()
        checksum = hashlib.sha256(data).hexdigest()
        packaged = {**manifest, 'checksum': checksum}
        encoded = (json.dumps(packaged, indent=2) + '\n').encode()
        (output / f'manifest-{goos}-{goarch}.json').write_bytes(encoded)
        filename = f'silo-plugin-mediux-{version}-{goos}-{goarch}.zip'
        with zipfile.ZipFile(output / filename, 'w') as archive:
            archive_entry(archive, 'manifest.json', encoded, 0o100644)
            archive_entry(archive, 'plugin', data, 0o100755)
            for license_file in ('LICENSE', 'OFL-Baloo2.txt'):
                archive_entry(archive, license_file, (ROOT / license_file).read_bytes(), 0o100644)
        checksums.append(f'{checksum}  {binary.name}')
        checksums.append(f'{hashlib.sha256((output / filename).read_bytes()).hexdigest()}  {filename}')
        print(f'Packaged {platform}: {filename}', flush=True)
    (output / 'checksums.txt').write_text('\n'.join(sorted(checksums)) + '\n')


if __name__ == '__main__':
    main()
