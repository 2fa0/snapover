<p align="center">
  <img src="resources/app.png" alt="Snapover" width="96" height="96" />
</p>

<h1 align="center">Snapover</h1>

<p align="center">
  <strong>One clipboard for two computers.</strong><br>
  Copy text, images, files or folders on one machine and paste them on the other<br>
  over your local network. Native on macOS and Windows, never in the cloud.
</p>

<p align="center">
  <a href="#-highlights">Highlights</a> •
  <a href="#-installation">Installation</a> •
  <a href="#-pairing-for-the-first-time">Pairing</a> •
  <a href="#-shortcuts">Shortcuts</a> •
  <a href="#-command-line">CLI</a> •
  <a href="#-build-from-source">Build</a> •
  <a href="#-privacy-and-security">Privacy</a> •
  <a href="#-license">License</a>
</p>

<p align="center">
  <a href="https://github.com/2fa0/snapover/releases"><img src="https://img.shields.io/github/v/release/2fa0/snapover?style=flat-square&color=blue" alt="Release" /></a>
  <img src="https://img.shields.io/badge/Go-1.27+-00ADD8.svg?style=flat-square&logo=go" alt="Go" />
  <img src="https://img.shields.io/badge/Platform-macOS%2013%2B%20%7C%20Windows%2010%2F11-brightgreen.svg?style=flat-square" alt="Platform" />
  <img src="https://img.shields.io/badge/License-MIT-yellow.svg?style=flat-square" alt="License" />
  <img src="https://img.shields.io/badge/Built%20with-MyGo-6E4AFF.svg?style=flat-square" alt="MyGo" />
</p>

<p align="center">
  <strong>English</strong> | <a href="README_zh.md">简体中文</a>
</p>

---

## 💡 Why Snapover

Copy something on computer A, paste it on computer B. No cloud drive, no account, nothing exposed to the internet.

Both computers discover each other on the same local network and connect directly over a self-signed TLS 1.3 channel. The first pairing asks you to compare a six-digit code bound to that handshake and confirm on both sides; only then is the certificate fingerprint pinned, and every later connection is checked against it. No accounts, no server to maintain, no copies left anywhere.

---

## ⚡ Highlights

- 📋 **Copy and it arrives**: text is UTF-8, 1 MB by default and adjustable up to 20 MB in Settings. Line endings are normalised per platform (LF internally, CRLF on Windows) while blank lines, indentation, trailing newlines and lone CRs survive; a change of line-ending style is not treated as new content. Content written by the remote side is never synced back.
- 🖼️ **Images**: converted to PNG for transport, capped at 20 MB and 40 megapixels; anything larger is skipped and logged rather than synced.
- 📁 **Files and folders**: copy them in Finder or Explorer and paste on the other machine — no send dialog involved. Directory structure and empty folders are preserved, nothing needs zipping, and cutting still behaves like copying (the source is not deleted). 20 MB per batch by default, adjustable.
- 🔗 **No middleman**: a direct LAN connection with a single active peer at a time. Virtual network interfaces are ignored by default, so a VPN address never becomes the address Snapover tries next time.
- 🔒 **Pairing needs both sides**: rejection, cancellation and timeout never fall back to trust; unpairing disconnects immediately and revokes trust, so a peer holding the old record still has to be confirmed again.
- ⚡ **Quick history**: `⌥⌘V` (`Alt+V` on Windows) opens a searchable history; arrow keys and Enter or a double click paste straight into the field you were just in. The first nine pinned items get `⌘1`–`⌘9`.
- 🗂️ **Clipboard history**: background full-text search with highlighted matches, pinning, per-item notes, hover image previews with a zoom viewer, a full-text window for long entries. Unpinned items keep for 1000 entries or 30 days within a 2 GB budget by default; pinned items export and import as a ZIP backup.
- 🎯 **Automatic or manual sync**: in manual mode a normal copy stays local, and `⌥⌘C` / `Ctrl+Alt+C` sends the current entry when you ask.
- 🕵️ **Privacy on your terms**: content marked by password managers is neither stored nor synced; pause sync, sync text only, or exclude source applications. Privacy mode survives a restart.
- 🌐 **Across networks, optionally**: enable Tailscale support on both sides, and the LAN and Tailscale addresses are stored separately — a returning LAN link never preempts an established tunnel.
- 📤 **Send files on purpose**: Windows "Send to", the macOS Finder service, or the CLI drops files into the other machine's receiving folder without touching the clipboard; no size limit in the app, with pause, resume and reconnect.
- 💻 **Command line**: `snapover-cli` reuses the pairing and connection the desktop app already holds, for scripts and terminal workflows.
- 🔄 **Built-in updates**: releases are signed with Ed25519 with the public key compiled in, and delta updates fall back to the full archive when they cannot be applied.
- 🌍 **English and Chinese interface**, following the system by default; Windows also gets a keep-awake switch.
- 🪶 **A native app**: Go and MyGo draw real native controls rather than embedding a browser, so an idle window does not cost hundreds of megabytes.

Detailed behaviour, limits and measured notes live in [features and behaviour](docs/features.md), alongside the [CLI reference](docs/cli.md) and the [build notes](docs/build.md).

---

## 📦 Installation

### macOS (Apple silicon & Intel)

1. Download the matching `.dmg` from [**Releases**](https://github.com/2fa0/snapover/releases):
   - **Apple silicon (M1/M2/M3/M4)**: `snapover-<version>-arm64.dmg`
   - **Intel x86_64**: `snapover-<version>-amd64.dmg`
2. Open the disk image and drag **Snapover** into `Applications`.

> [!NOTE]
> **First launch**: the app is ad-hoc signed and not notarised yet. Right-click (or Control-click) `Snapover.app` and choose **Open**, or run:
> ```sh
> xattr -cr /Applications/Snapover.app
> ```
> Without a Developer ID, a build opens directly only on the Mac that produced it.

### Windows (10 / 11 x64)

- **ZIP (portable)**: download `Snapover-<version>-windows11-x64.zip`, extract it, and run `Snapover.exe` from the folder — keep the files next to it together.
- **Portable data**: drop an empty `portable.flag` next to `Snapover.exe` and data goes to `data/` beside it instead of your user profile.
- The Windows build is not code signed, so SmartScreen may intervene; choose **More info → Run anyway**.
- Allow inbound TCP `47821` through the firewall; discovery also needs UDP `5353`. Before pairing, the "Connect devices" page can add the firewall rule for you.

> [!IMPORTANT]
> **Releases can lag behind the default branch.** This README describes `main` (`0.10.1`), where file clipboard sync, sending files, Tailscale support and resumable transfers are not released yet. To use them today, [build from source](#-build-from-source).

---

## 🚀 Pairing for the first time

1. Install and launch Snapover on both computers. It stays in the tray or menu bar; closing the window does not quit it.
2. Open **Connect devices** → **Add device** and pick the computer that appears. If discovery does not find it, enter the address by hand — port `47821` is assumed.
3. Compare the **six-digit code** shown on both computers and confirm on each of them.
4. The device and its address are remembered, so the next launch reconnects automatically, preferring the most recent device and trying the other paired devices in the same round if that one is unreachable.

Notes:

- The add window stays open for 2 minutes and a single confirmation waits up to 90 seconds. Rejection, cancellation or timeout never leads to trust, and nothing is synced or received before both sides confirm.
- macOS 15 and later asks for **Local Network** access the first time discovery runs — allow it. macOS 13 and 14 have no such entry. Accessibility permission is only requested the first time you use quick paste.
- If broadcast is blocked by network isolation, a VPN or system permissions, enter the address by hand; **Connect devices** can also limit which subnets take part in discovery.
- **Test connection** only checks that the TCP port is reachable, not TLS or pairing.

Details on multiple devices, address priority and cross-network use are in [features and behaviour](docs/features.md#tailscale-across-networks).

---

## ⌘ Shortcuts

| Action | macOS | Windows |
| --- | --- | --- |
| Open quick history | `⌥⌘V` or `Cmd+Shift+V` | `Alt+V` |
| Copy and sync (manual mode) | `⌥⌘C` | `Ctrl+Alt+C` |
| Paste one of the first nine pinned items | `⌘1`–`⌘9` | `Ctrl+1`–`Ctrl+9` |
| Write to the clipboard without pasting | `⌥Enter` | `Alt+Enter` |
| Pin or unpin the current item | `⌘⇧P` | `Ctrl+Shift+P` |
| Delete the current item | `⌘⇧⌫` | `Ctrl+Shift+Backspace` |

Enter or a double click in quick history pastes directly, while a plain Backspace still edits the search field. Every shortcut can be recorded, saved or reset in Settings; a conflicting combination keeps the previous one. Quick paste needs Snapover allowed under System Settings → Privacy & Security → Accessibility.

---

## 💻 Command line

`snapover-cli` reuses the pairing and the current connection of the desktop app, so there is nothing to pair again and no send window opens. **Start Snapover and connect a device first**, then:

```sh
# macOS: alias the helper inside the installed app
alias snapover='/Applications/Snapover.app/Contents/Resources/snapover-cli'

snapover send file './report.pdf' './screenshot.png'
snapover send text 'hello'
snapover send image './screenshot.jpg'
cat ./notes.txt | snapover send text --stdin
```

```powershell
# Windows PowerShell: add the extracted folder to PATH, or set an alias
Set-Alias snapover "C:\Apps\Snapover\snapover-cli.exe"

snapover send file "C:\Users\me\Documents\report.pdf"
Get-Content -Raw -Encoding UTF8 .\notes.txt | snapover send text --stdin
```

That is enough to remember: files land in the other machine's receiving folder, text and images go to its clipboard. Exit code `0` on success, `1` when sending or connecting fails, `2` for bad arguments; with no online device the command fails immediately instead of queueing.

Full options, encoding caveats, exit codes and timeout rules: [sending from the command line](docs/cli.md).

---

## 🔄 Built-in updates

**Settings → About & updates** checks on demand, and every 24 hours in the background by default. When a new version exists, the update window shows the release notes with install, remind me later and skip this version; the tray menu has **Check for updates…** too.

Releases are signed with an Ed25519 key whose public half is compiled into the app, and a signature or size mismatch leaves the app untouched. Delta updates are generated from the last three releases and fall back to the full archive. On macOS the whole `.app` is replaced after quitting; on Windows the files are replaced in place and leftovers are cleaned on the next launch, without disturbing `portable.flag`, `data/` or neighbouring files.

Cases that cannot self-update say why instead of failing silently: an app run straight from a disk image, one in a non-writable location, and source builds (`go run` / `go build`) all need a new package installed by hand. macOS builds are ad-hoc signed for now, so after an update you may need to allow Accessibility again.

How maintainers package and publish, and how the signing key must be kept, is in [building and releasing](docs/build.md); the plan and its open items are in the update plan (Chinese: [update-plan.md](docs/update-plan.md)).

---

## 🚧 Known limitations

- **Image formats**: MyGo's `WriteImage` writes PNG. Windows additionally gets a 24-bit, bottom-up `CF_DIB` rather than `CF_DIBV5`, and macOS does not also write TIFF. Screenshots and plain text are the acceptance bar; the alpha channel may be lost when pasting into WeChat, Word, Photoshop or Feishu.
- **Remote desktop**: when using Windows over RDP, turn off clipboard sharing/redirection for that connection and let Snapover handle the copy. `rdpclip.exe` can overwrite the clipboard after Snapover writes it, so `Ctrl+V` does nothing while pasting from history still works; Snapover detects the new owner and suggests turning the shared clipboard off rather than writing twice. Disabling RDP clipboard sync is a confirmed fix.
- **Network changes**: switching Wi-Fi rebinds the physical interface and updates paired addresses through discovery; where multicast is blocked, enter the address by hand.
- **Signing**: without a Developer ID a macOS build opens only on the machine that produced it; Windows builds are unsigned, so SmartScreen may intervene.
- **Not planned**: built-in discovery or relaying across the internet, and rich-text sync. For cross-network use, bring your own Tailscale and allow it in Settings.

---

## 🔧 Build from source

Requires **Go 1.27.3+** (see `go.mod`) and no cgo.

```sh
git clone https://github.com/2fa0/snapover.git
cd snapover

go test ./...
go run ./cmd/snapover
```

Or just build the binary:

```sh
go build -o bin/snapover ./cmd/snapover     # on Windows name it bin/Snapover.exe
```

What you get is a development build: no release update configuration, so it cannot self-update, and system integrations such as the Finder service need a packaged app.

Packaging all three platforms is a maintainer step and needs the official update signing key:

```sh
./build.command
```

Outputs: `dist/Snapover-<version>-windows11-x64.zip`, the `.app` and `.dmg` bundles under `dist/darwin-arm64/` and `dist/darwin-amd64/`, plus update archives and manifests in `dist/releases/`. For local testing use `./build-dev.command`, which skips delta generation and writes to `dist/dev/`.

One platform at a time:

```sh
go tool mygo build -platform darwin/universal -o dist     # universal .app + .dmg
go tool mygo build -platform windows/amd64 -o dist        # cross-compile Windows x64
```

The real-network discovery test only runs when asked:

```sh
SNAPOVER_TEST_LAN=1 go test -count=1 ./internal/discovery -run TestLANDiscovery -v
```

> [!NOTE]
> Cross-compiling only proves the other platform compiles; it is not a real-device test. A Windows build made on a Mac cannot verify clipboard behaviour — `Win+Shift+S` and pasting into Notepad have to be tried on Windows. When tests are launched from a desktop host app on macOS, local network permission belongs to that host, so `no route to host` alone does not prove a routing problem.

Packaging details, signing keys and the release checklist: [building and releasing](docs/build.md).

---

## 🔐 Privacy and security

- **Data stays local**: `~/Library/Application Support/Snapover` on macOS, `%AppData%\Snapover` on Windows. It holds the machine certificate, `peers.json`, history and logs. **Do not copy `identity/` to another machine.**
- **Logs carry no content**: `logs/snapover.log` is the run log and `logs/crash.log` the crash log; both rotate at about 1 MB keeping one previous file, and entries older than seven days are removed. Logs never contain clipboard text, image bytes or private keys.
- **No telemetry**: nothing is reported or uploaded automatically. A connection diagnosis summary contains the platform, connection state and result categories — no clipboard content, paths, device names, addresses or certificates.
- **Reporting security issues**: please use [GitHub private vulnerability reporting](https://github.com/2fa0/snapover/security/advisories/new) instead of a public issue, and strip credentials and private content from any logs or samples you attach.

History budgets, caches and crash triage: [features and behaviour](docs/features.md).

---

## 🤝 Contributing

Issues and pull requests are welcome.

1. Fork the repository and branch from `main` (`git checkout -b feat/your-feature`).
2. Small changes can go straight to a pull request; for protocol, persistence, concurrency or pairing security, describe the constraints in an issue first.
3. Run the relevant `go test` before submitting, and look at UI changes on a real macOS or Windows machine.
4. Never commit the update signing key, build outputs (`bin/`, `dist/`) or logs containing real clipboard content.

Code layout, behaviours that must be preserved and the verification commands are in [AGENTS.md](AGENTS.md).

---

## 📄 License

Snapover is released under the [MIT License](LICENSE), copyright 2fa0.

In short: use, copy, modify, merge, publish, distribute, sublicense and sell — commercially or not — as long as the copyright and permission notices are kept in all copies. The software comes "as is", without warranty of any kind.

Using it for free, forking it, renaming it and shipping it with your own icon is all welcome, and contributions back are appreciated. Repackaging Snapover into a paid product is discouraged; with MIT that is no longer something the license forbids, so it is a request, not a condition.

Third-party components and icons keep their own licenses, unchanged by this project's MIT license: see `cmd/snapover/icons/LICENSE*` and `cmd/snapover/licenses/` for icons, and [THIRD_PARTY_NOTICES.txt](resources/THIRD_PARTY_NOTICES.txt) for the full list. `LICENSE` and `THIRD_PARTY_NOTICES.txt` are shipped with the app too — under `Snapover.app/Contents/Resources/` on macOS, next to the executable on Windows.
