"""Build only Grim Poster sources. Requires Python, Node and Go 1.23+."""
from pathlib import Path
import os
import sys
import subprocess
import zipfile

root = Path(__file__).resolve().parent.parent
os.chdir(root)
source = (root / 'src/grim-poster.js').read_text() + '\n' + (root / 'src/launcher-position.js').read_text()
# Restrict hooks to the official app origin, including after page navigation.
source = '(() => { if (location.origin !== "http://tauri.localhost") return;\n' + source + '\n})();\n'
(root / 'launcher-src/runtime.js').write_text(source)
with zipfile.ZipFile(root / 'launcher-src/bundled.zip', 'w', zipfile.ZIP_DEFLATED) as bundle:
    for name in ['desktop/patch/patch-manifest.json', 'desktop/GrimPoster.ico']:
        info = zipfile.ZipInfo(name, (2026, 1, 1, 0, 0, 0))
        info.compress_type = zipfile.ZIP_DEFLATED
        info.external_attr = 0o644 << 16
        bundle.writestr(info, (root / name).read_bytes())
subprocess.run([sys.executable, 'tools/build-icon-resource.py', '--icon', 'GrimPoster.ico', '--output', 'launcher-src/rsrc_windows_amd64.syso', '--version', '0.2.0'], check=True)
subprocess.run(['node', '--test', *sorted(str(p.relative_to(root)) for p in (root/'tests').glob('*.test.mjs'))], check=True)
env = dict(os.environ, GO111MODULE='off', CGO_ENABLED='0')
subprocess.run(['go', 'test', './...'], cwd=root/'launcher-src', env=env, check=True)
(root/'dist').mkdir(exist_ok=True)
env.update(GOOS='windows',GOARCH='amd64')
subprocess.run(['go','build','-trimpath','-buildvcs=false','-ldflags=-H=windowsgui -s -w -buildid=','-o','../dist/GrimPoster-0.2.0-unsigned.exe','.'], cwd=root/'launcher-src', env=env, check=True)
