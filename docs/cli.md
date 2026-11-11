# Sending from the command line

`snapover-cli` reuses the pairing and the current connection of the desktop
app. A new `snapover-cli` needs a matching new backend; older backends reject its
requests. Start Snapover, connect a device, then run the CLI — no pairing again,
and no send window. With no online device it fails immediately instead of
queueing until the next connection.

`build.command` places `snapover-cli.exe` next to `Snapover.exe` on Windows and
`snapover-cli` inside `.app/Contents/Resources/` on macOS, shipping both with the
ZIP and DMG. The Windows GUI executable is already `Snapover.exe` and file names
are case-insensitive there, hence the separate CLI name.

**English** | [简体中文](cli_zh.md)

## Windows (PowerShell)

Add the extracted folder to PATH and call `snapover-cli`, or define a short
alias (use your real path):

```powershell
Set-Alias snapover "C:\Apps\Snapover\snapover-cli.exe"

snapover send file "C:\Users\xxx\Documents\report.pdf"
snapover send file "C:\report.pdf" "C:\screenshot.png"
snapover send text "hello"
snapover send image "C:\Users\xxx\Pictures\screenshot.png"
Get-Content -Raw -Encoding UTF8 .\notes.txt | snapover send text --stdin
```

Windows PowerShell 5.1 needs `$OutputEncoding = [System.Text.UTF8Encoding]::new()`
before piping text to native programs; PowerShell 7 is UTF-8 by default. To read
the CLI's Chinese output through a pipe in 5.1, set `[Console]::OutputEncoding` to
the same UTF-8 encoding. See
[about_character_encoding](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_character_encoding).
Add the `Set-Alias` line to `$PROFILE` to keep it in new terminals.

## macOS (zsh)

```zsh
alias snapover='/Applications/Snapover.app/Contents/Resources/snapover-cli'

snapover send file './report.pdf'
snapover send file './report.pdf' './screenshot.png'
snapover send text 'hello'
snapover send image './screenshot.jpg'
cat ./notes.txt | snapover send text --stdin
```

Adding the `alias` line to `~/.zshrc` makes it available in new terminals. You can
also run the CLI by full path or put its directory on PATH and call
`snapover-cli`.

## Behaviour

Files are saved into the peer's receiving folder. Sending has no size limit in
the app, bounded by the receiver's free space and filesystem. You can pass
several paths and they transfer in the given order; the first failure stops the
batch and reports what completed, and every file needs the peer's save
confirmation, so a batch never half-succeeds. Text and images go to the peer's
clipboard without changing the sender's; successfully sent content enters both
sides' history with the source `Snapover CLI`.

The text limit follows Snapover's setting (1 MB by default, up to 20 MB) and the
receiver validates its own limit too. Piped input must be UTF-8, and blank
lines, indentation and trailing newlines are preserved with the usual
cross-platform line-ending rules. Images may be PNG, JPEG, BMP or WebP, and
neither the original nor the converted PNG may exceed 20 MB, with the existing
40-megapixel limit; use `send file` to save an image as a file instead of putting
it on the clipboard.

The CLI sends to the device connected when the request starts; changing device
or disconnecting fails instead of sending to another device. Files show batch
progress and can be given several paths at once; text and images report success
only after the peer confirms writing to its clipboard. Text and images respect
the local pause, text-only and image-size settings, and report the reason when
the peer is paused, has just copied something or fails to write the clipboard.
Sending files ignores clipboard sync switches.

Text and image sending requires the peer to run a version with receive
confirmations; older peers keep working for ordinary clipboard sync. When the
peer has just copied something, the content only enters its history, the CLI
reports that the clipboard was not written and exits non-zero. There is no total
transfer timeout and no `--timeout` option: while bytes keep moving it
continues, and after 15 minutes without progress it stops and reports why,
including while waiting for the receiver's confirmation. Multi-file progress is
tracked by accumulated bytes, and repeated identical progress does not extend
the wait. A timeout or Ctrl+C stops the transfer and closes the shared
connection when needed, after which the background app reconnects as usual. When
no confirmation arrives, check the peer's clipboard or receiving folder before
retrying.

## Options and exit codes

Options go before file paths or text; put `--` before text that starts with `-`:

```sh
snapover send file './big.zip'
snapover send text -- '-text starting with a dash'
snapover send text --data '/custom/Snapover data' 'hello'
snapover-cli --help
```

The CLI reads the same data directory as the desktop app by default. A portable
install just keeps `snapover-cli.exe` next to `portable.flag`; if the desktop app
was started with `-data`, pass the same directory to the CLI.

Exit code `0` means success, `1` a failed send or connection and `2` bad
arguments or input. Success messages go to standard output while progress and
errors go to standard error, which keeps scripts simple.

## Building the CLI from source

The CLI is a console program and needs no MyGo windowing:

```sh
# macOS
go build -o bin/snapover-cli ./cmd/snapover-cli

# Cross-compile Windows x64 from a Mac
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o bin/snapover-cli.exe ./cmd/snapover-cli
```
