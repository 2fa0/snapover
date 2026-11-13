# Building and releasing

For maintainers. Day-to-day development is `go test ./...` and
`go run ./cmd/snapover`; the README is written for users.

**English** | [简体中文](build_zh.md)

## Environment

- Go 1.27.3 or a compatible newer version; see `go.mod` for the exact
  requirement. No cgo.
- MyGo is pinned to v0.9.0.
- Packaging runs on macOS only.

## One-shot packaging

```sh
./build.command
```

This produces Windows 10/11 x64, Apple silicon (arm64) and Intel (amd64) packages;
macOS requires 13 or later:

- `dist/Snapover-<version>-windows-x64.zip` — extract and run `Snapover.exe`
  from the folder, keeping the resource files beside it
- `dist/darwin-arm64/Snapover.app` and
  `dist/darwin-arm64/snapover-<version>-arm64.dmg`
- `dist/darwin-amd64/Snapover.app` and
  `dist/darwin-amd64/snapover-<version>-amd64.dmg`
- `dist/releases/` — `*.tar.gz`, `*.delta`, `update-*.json`, `install.sh`

The version in file names comes from `mygo.json`. The Windows step avoids NSIS
and produces a ZIP, not a Setup installer; macOS keeps the current signing
configuration.

For local testing run `./build-dev.command`: it still builds all three platforms
but skips delta generation and writes to `dist/dev/`, leaving release artifacts
alone. The signing key is still required.

## Update signing key

`build.command` checks the update signing key (`updates.privateKey` in
`mygo.json`, or `MYGO_UPDATER_PRIVATE_KEY`) and stops if it is missing — a build
without it carries no manifest, so installed apps could never update to it.

The official key is held by the maintainer, never committed and never handed to
contributors; confirm before the first release that it is backed up in a password
manager. Losing it means installed users can never update again; leaking it is
equivalent to handing over release rights, leaving only a new key and a release
signed with the new public key.

Generate one with `go tool mygo keygen -o ~/.config/snapover/update-keys`.

## Building one platform

macOS (universal `.app` + `.dmg`):

```sh
go tool mygo build -platform darwin/universal -o dist
```

Output lands in `dist/darwin-universal/`. Without a Developer ID the build is
ad-hoc signed and opens directly only on the Mac that produced it; other Macs are
stopped by Gatekeeper.

Windows:

```sh
go tool mygo build -platform windows/amd64,windows/arm64 -o dist
```

- `dist/windows-amd64/Snapover.exe`
- `dist/windows-arm64/Snapover.exe`

The Windows build is a windowed program with no console and the same interface
as macOS. Homebrew's `makensis` crashes on this Mac, which is why no Setup
installer is produced; copy the executable to Windows and run it.

Cross-compiling the Windows CLI from a Mac:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/snapover-cli.exe ./cmd/snapover-cli
```

## Releasing a version

1. Bump `version` in `mygo.json` and add the matching `## x.y.z` section to
   `docs/CHANGELOG.md` — the update window reads it from there and the build
   fails without it.
2. Run `./build.command`.
3. Create a draft release in the repository named by `updates.github`, with the
   tag matching the version (`v<version>`, for example `v0.8.1`). Upload this
   version's archives, deltas and install script from `dist/releases/`, the
   Windows ZIP from `dist/`, and the DMGs from `dist/darwin-arm64/` and
   `dist/darwin-amd64/`, then upload the freshly generated `update-*.json` last.
   The directory may still hold older files — do not mix them in. Publish only
   once all three targets are complete.

Download URLs in the manifest contain that tag and no other may be used. Keep
published files of older versions: deltas are generated from them.

The update URL is compiled into the app, so changing the release repository
requires a rebuild; already installed copies keep the old address and need one
manual install of a new package.


## Resources and icons

Icon code and images live in `internal/resources/`; `resources/` holds only files
shipped with the app, so source is never copied into the package.

The app icon is `resources/app.png`. The menu bar and tray icon can be chosen in
Settings among mailbox, bird and envelope — `internal/resources/icon2.png`,
`internal/resources/icon1.png` and `internal/resources/icon3.png`, which must keep
their alpha channel; replacing them requires repackaging and restarting.

`resources/THIRD_PARTY_NOTICES.txt` ships with the whole `resources/` directory;
update that list when upgrading dependencies or swapping third-party assets. The
package also carries `LICENSE`: Apache-2.0 requires the license text to travel
with every distribution, so it stays in the `resources` list in `mygo.json`.

## Verification

```sh
# A single behaviour or package; replace the package and test name
go test ./internal/clip -run TestName -count=1
go test ./internal/sync ./internal/history
go test ./cmd/snapover ./cmd/snapover-cli

# When the change is broad
go test ./...
go vet ./...

# Format only the Go files you changed
gofmt -w path/to/changed.go
git diff --check
```

Verify real windows — closing and rebuilding, background updates, quick-history
thumbnails and file requests from the system context menu — from a desktop
session:

```sh
SNAPOVER_TEST_UI=1 go test ./cmd/snapover -run TestMainWindow -count=1
```

The test uses a temporary data directory.

The real-network discovery test uses the actual network, so run it only when
verifying discovery:

```sh
SNAPOVER_TEST_LAN=1 go test -count=1 ./internal/discovery -run TestLANDiscovery -v
```

When tests are launched from a desktop host app such as Cindy on macOS, local
network permission belongs to that host: allow it under System Settings →
Privacy & Security → Local Network, or run from Terminal. `no route to host` can
also come from a denied local network permission, so it alone does not prove a
VPN or routing problem. The test waits for the first permission and retries,
failing only when nothing is discovered before the timeout; the underlying error
stays in the output. Snapover's own discovery errors go to `logs/snapover.log`
without repeating forever.

Cross-compiling only proves the target platform compiles; it is not a substitute
for testing the clipboard, shortcuts, permissions or windows on real hardware. A
Windows build made on a Mac cannot verify clipboard behaviour — `Win+Shift+S`
and pasting into Notepad have to be tried on Windows.

### Checks that need a desktop session

These run only with the environment variable set, so a plain `go test ./...`
skips them. Run them by hand when changing the surrounding code; none of them is
covered by the default run.

`SNAPOVER_TEST_UI=1`:

| Test | What it verifies |
| --- | --- |
| `TestCheckModeLeavesRunningAppDataUntouched` | Diagnostic startup with a fake clipboard leaves the running app’s files intact and omits clipboard contents |
| `TestMainHistoryPreviewLoadsAndKeepsMainWindow` | Hover preview for text and images, full text and image viewer replacing it, no reopen after the window closes |
| `TestMainWindowClosesAndReopensWithoutStoppingBackgroundUpdates` | Closing and rebuilding the main window keeps background updates running |
| `TestMainWindowClosedPickerLoadsAndFileRequestsStillWork` | Quick history and file requests while the main window is closed |
| `TestMainWindowClosedHistoryMaintenanceStillCompletes` | History maintenance finishing after the main window closed |
| `TestPickerTextWindowsLoadAndDismiss` | Quick-history text preview after the hover delay, full text replacing it, segmented long text |
| `TestNativeQuickHistoryFolderPreviewAndFileNotices` | Folder preview in quick history and local/remote file notices |
| `TestViewerReplacementWaitsForCanceledLoad` | Image viewer replacement waiting for a cancelled load |
| `TestThemeSettingsSwitchAndPersist` | Appearance switching and persistence |
| `TestNativeLanguageFollowsSettings` | Interface language following the system setting |
| `TestShortcutRecorderNativeMenuRestoredAfterCancelAndClose` | The application menu is restored after recording is cancelled or closed |
| `TestPickerShortcutNativeChangeKeepsWorkingKeyOnConflict` | A native shortcut conflict keeps the previous working key |
| `TestNetworkGuideWindowReleasesStateAndCanBeRecreated` | The network guide window releases its state and can be rebuilt |
| `TestMacNetworkGuideSettingsLaunchAndCancellation` | macOS: opening System Settings for local network, and cancelling it |
| `TestMacFileRequestReachesRunningInstanceWithoutOpeningMainWindow` | macOS: Finder sends files to the running instance |
| `TestFinderServiceUsesExistingFileRequestAndRespectsDisable` | macOS: the Finder service respects being turned off |
| `TestNativeServicePreservesPathsAndDoesNotUseGeneralClipboard` | macOS: the Finder service passes paths without touching the clipboard |

`SNAPOVER_TEST_LAN=1` runs `TestLANDiscovery` against the real network. A denied
local network permission fails that test; allow the host app or run it from
Terminal before treating it as a discovery defect.

## Artifacts and the repository

`bin/`, `dist/` and generated CLI binaries are build artifacts and are not
committed. A Windows test binary can be compiled on a Mac and run on Windows:

```sh
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o /tmp/snapover-clip-windows.test.exe ./internal/clip
```
