#!/usr/bin/env python3
"""Build reproducible, checksummed CLI archives; no credentials or uploads."""
import argparse
import hashlib
import gzip
import io
import os
from pathlib import Path
import re
import subprocess
import tarfile
import zipfile

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser()
parser.add_argument('--version', required=True)
parser.add_argument('--commit', required=True)
parser.add_argument('--go', default='go')
args = parser.parse_args()
if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?', args.version):
    raise SystemExit('Use a semantic release tag, such as v0.1.0')
if not re.fullmatch(r'[a-f0-9]{7,40}', args.commit):
    raise SystemExit('Use a Git commit ID')
output = root / 'dist' / args.version
output.mkdir(parents=True, exist_ok=True)
checksums = []
for system in ('linux', 'darwin', 'windows'):
    for arch in ('amd64', 'arm64'):
        name = 'runivo.exe' if system == 'windows' else 'runivo'
        binary = output / f'{system}-{arch}' / name
        binary.parent.mkdir(exist_ok=True)
        env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED='0')
        subprocess.run([args.go, 'build', '-trimpath', '-buildvcs=false', '-ldflags',
            f'-s -w -X main.version={args.version} -X main.commit={args.commit}',
            '-o', str(binary), './cmd/runivo'], cwd=root, env=env, check=True)
        stem = f'runivo_{args.version}_{system}_{arch}'
        files = {name: binary.read_bytes(), 'LICENSE': (root/'LICENSE').read_bytes(), 'README.md': (root/'README.md').read_bytes()}
        if system == 'windows':
            archive = output / (stem + '.zip')
            with zipfile.ZipFile(archive, 'w', compression=zipfile.ZIP_DEFLATED) as package:
                for filename, data in files.items():
                    info = zipfile.ZipInfo(filename, (2026, 1, 1, 0, 0, 0))
                    info.compress_type = zipfile.ZIP_DEFLATED
                    package.writestr(info, data)
        else:
            archive = output / (stem + '.tar.gz')
            with archive.open('wb') as output_file, gzip.GzipFile(filename='', fileobj=output_file, mode='wb', mtime=0) as compressed, tarfile.open(fileobj=compressed, mode='w') as package:
                for filename, data in files.items():
                    info = tarfile.TarInfo(filename)
                    info.size = len(data)
                    info.mode = 0o755 if filename == name else 0o644
                    info.mtime = 0
                    package.addfile(info, io.BytesIO(data))
        checksums.append(hashlib.sha256(archive.read_bytes()).hexdigest() + '  ' + archive.name)
        print(archive.name, flush=True)
(output/'checksums.txt').write_text('\n'.join(checksums)+'\n', encoding='utf-8')
