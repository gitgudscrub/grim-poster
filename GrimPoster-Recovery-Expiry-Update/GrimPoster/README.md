# Grim Poster 0.2.0 — public-source preview

Grim Poster adds final-grimoire capture, privacy filtering and Discord posting to Blood on the Clocktower Online on Windows. This independent community project is maintained by Ryan Mavin and is not an official Blood on the Clocktower product.

This preview loads Grim Poster's JavaScript into the running BOTC WebView2 window. The Windows launcher embeds only Grim Poster code, metadata and artwork. It uses the installed official game and leaves its executable intact. Launch BOTC through Grim Poster each time you want the capture controls.

## Preview status

The Windows x64 executable builds successfully on Linux. Automated tests pass. The new in-memory loading path has not been exercised in a real BOTC session on Windows, and capture, restart, camera permissions and Game 2 notification need a Windows playtest before a production release. This build is unsigned. Publishing this repository alone does not make Windows trust its executable.

This release verifies the exact official BOTC 3.58.0 executable. An unsupported version is refused. Reinstall the official game first if your existing installation was modified by Grim Poster 0.1.x. This preview does not automatically restore an old patch. Preserve your old installer/uninstaller until the migration has been checked.

The v0.1.x patch update feed is disabled for this runtime release. Update the launcher by installing a newly built and signed executable. The manager still checks official BOTC releases, but updating BOTC to an unverified version can require a compatible Grim Poster release.

## Build

Install Go 1.23.12, Node.js 22 and Python 3.12. From this directory run `python tools/build-public.py` (or `python3` on Linux). It generates the embed files and icon resource, runs the Go and JavaScript checks, and builds `dist/GrimPoster-0.2.0-unsigned.exe`. The Windows executable uses only the Go standard library. The same source and toolchain produce the same unsigned executable.

The included GitHub Actions workflow builds an unsigned downloadable artifact. It never publishes a release or submits a signing request. Go tests can be rerun after generating the embed files with `GO111MODULE=off go test ./...` in `launcher-src`.

## Source layout

- `src` contains the capture UI and movable launcher button.
- `launcher-src` contains the native Windows manager, capture companion, diagnostics and tests. Some legacy patch/recovery helpers remain for their regression tests; the v0.2 manager does not invoke them.
- `tools/build-public.py` is the supported build entry point. It creates an allowlisted resource ZIP with only the manifest and Grim Poster icon.
- `desktop` includes historical PowerShell sources for reference and regression tests. These scripts are not included in the new executable. Use the native launcher for this preview.
- `relay` contains reference server code and unit tests. It requires a separate Sharktower `grim_relay_notifier` integration, which is absent from this archive. It is not a standalone, ready-to-deploy server. The desktop build uses the existing Sharktower service.

## Privacy and services

Grim Poster connects to `updates.themavryan.com` for grimoire staging/posting, Game 2 requests. The capture workflow can stage a grimoire image on the relay before final publication. The reference relay holds staged images in memory with an expiry. Successful posts go to the server-configured Discord destination. Review these flows before distributing a fork; the current service is operated for Sharktower.

The public desktop app does not submit diagnostic reports. The manager retains View logs for local troubleshooting. The reference relay still includes a legacy diagnostic endpoint used for maintainer testing; removing desktop submission does not disable that server endpoint or older clients. Local logs are kept under `%LOCALAPPDATA%\\Programs\\Grim Poster\\logs`. Game/account data is managed by BOTC. The capture listener binds to loopback and accepts the app origin. The companion enables a local WebView2 debugging endpoint for its game session. This endpoint is not a boundary against other local processes.

## Code signing policy

Signing-provider approval is pending. This repository and its executable are not currently signed or endorsed by SignPath. See SIGNING.md for the approval and signing steps.

## Licence

MIT, copyright 2026 Ryan Mavin. This is the proposed licence for this source preparation; the maintainer should confirm it before making the repository public. BOTC application code and compressed application bundles are excluded. The game's names and other third-party marks remain their respective owners' property.

### Local recovery expiry

The recovery-expiry update expires pending local screenshots 24 hours after capture. Checks run on startup, every minute while open, and before sending or downloading. Closed or suspended apps clean up when they next run. Active uploads may finish first; failed storage deletion is retried. Invalid or missing capture times expire immediately. Manually downloaded files and Discord posts are not deleted.
