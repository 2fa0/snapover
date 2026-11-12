# Features and behaviour

This document expands the [README](../README.md). It is organised by topic and
reflects the current code and real-device testing. UI entry points and
shortcuts are described in the README.

**English** | [简体中文](features_zh.md)

## Text

- UTF-8, 1 MB by default, adjustable to 1–20 MB in Settings. The limit counts
  bytes: oversized text is neither stored in history nor transferred, while
  entries already saved can still be pasted.
- Line endings cross platforms: LF internally, CRLF when writing to the Windows
  clipboard, LF on macOS. Blank lines, indentation, trailing newlines and lone
  CRs survive, and a difference in line-ending style does not count as new
  content.
- Each side enforces its own text limit: a new limit applies immediately and
  both ends validate against their own setting.

## Images

- Images are converted to PNG for transport, up to 20 MB. Anything larger is
  skipped and logged. The limit is configurable up to a hard 20 MB; images over
  the limit stay in local history only.
- Decoding large images shares one budget: PNG is capped at 20 MB and 40
  megapixels, and the decoded pixel buffers at 160 MiB. Unusually large 16-bit
  RGBA images may therefore be rejected; ordinary 8-bit 5K screenshots still
  fit. TIFF and DIB payloads are size-checked before they are copied into Go,
  and conversion happens in the background. After a run of large images the app
  waits for idle before reclaiming memory instead of forcing a GC per image.

## Files and folders

Once both sides run a version with file clipboard support and are connected,
copy files or folders normally in Finder or Explorer (multi-selection works),
then paste on the other computer with `⌘V` / `Ctrl+V`. No send dialog is
involved. Copying an image file syncs it as a file; copying pixels inside an
image editor still syncs as an image.

- Files are transferred in chunks in the background and written to the peer's
  native file clipboard only after the whole batch is received and verified.
  Small batches finish silently; from about 20 MB, or when a transfer takes
  longer than roughly 1.5 seconds, the tray, the sidebar and a progress window
  that does not steal focus show packing, transfer progress, the peer
  processing and finally readiness to paste.
- Folders keep their structure, empty directories and file contents; nothing
  needs to be zipped or unpacked. Cutting is treated as copying and never
  deletes the source, and later changes to the original are not tracked.
- File clipboard sync follows the automatic/manual setting, pause and
  text-only mode. Copying something new during a transfer stops the old batch;
  if a newer copy happens locally or arrives from the peer while receiving, the
  older batch will not overwrite it. A disconnect cleans up unfinished
  receives; copy again after a failure. Peers on older versions still sync text
  and images and are told to update when files are copied.
- A batch is capped at 20 MB in total by default, adjustable under Settings →
  Clipboard sync → Size limits ("File copy sync limit"). Both sides validate
  their own value and existing custom values are kept. This limit does not
  apply to sending files on purpose.
- A batch holds at most 10000 files and folders and 64 levels of directories.
  Symlinks, special files and names that cannot be preserved safely across both
  platforms are not supported yet; duplicates of unsupported content fail the
  whole batch rather than publishing an incomplete tree.
- The file cache lives in `clipboard-files/` inside the data directory and never
  lands in the receiving folder. Cache referenced by the current system
  clipboard is kept so it can still be pasted after a restart; other completed
  caches last at least 15 minutes and are removed by later receives or the
  five-minute background cleanup. At most 128 batches are kept; before a new
  receive starts, the budget for existing content is fixed at 1 GiB and space
  for the incoming archive and its unpacked contents is reserved separately.
  Raising the size limit does not enlarge the old budget; when space runs out
  the app asks you to retry later instead of dropping current clipboard files.
- Files and folders also appear in the main window and quick history, with
  search, pinning, deletion and capacity cleanup. Files are listed by name and
  folders by name; hovering a folder row for 300 ms shows the tree as it was
  copied, expanded one level and capped at 20 entries, with a note that more
  exist. The preview height scales between roughly 280 and 400 pixels with the
  item count, scrolls when needed and shrinks on small screens. A
  multi-selection is one entry with the same one-level, 20-entry preview.
- History records the original path (or the `clipboard-files/` cache path), the
  name and one level of the tree. Unpinned entries store no file contents;
  pinning saves a copy within the history capacity and entry limits, and
  unpinning removes it. As long as the original path, the receive cache or the
  pinned copy exists, the entry can be copied or pasted from the main window or
  quick history, or revealed in Finder / Explorer (the original path wins).
  Once the path is gone and the entry is not pinned, the menu says to copy it
  again. Use the system context menu or the CLI to drop files straight into the
  peer's receiving folder; for everyday moving about, just copy and paste.

## Queue, confirmations and echo suppression

- Echo suppression: content written by the peer is never synced back. Remote
  writes carry a lightweight marker for the writing process; the listener
  checks the marker and sequence number first, so it does not decode images for
  its own writes. If the clipboard changes while reading, it samples again
  rather than attributing new content to an old sequence number.
- Rapid clipboard changes wait for a stable value before being recorded and
  synced; when Excel, Figma or an IDE writes many times per second only the last
  one survives. The stable window drops intermediate values on purpose, which
  is what merges repeated writes from spreadsheet and design tools.
- The outgoing queue holds at most 8 entries totalling 20 MiB. Reaching either
  limit on a slow link reports a full queue while that copy stays in local
  history. Pending content is released when the connection closes.
- Automatic sync tracks the peer's confirmation per entry and shows the latest
  result in Settings. A confirmed write shows no notification; pause, write
  failure, timeout or an unconfirmed disconnect show the reason. Only a few IDs
  are tracked, and no extra copies of text or images are kept.
- If you copied something locally within the last two seconds, incoming content
  only goes to local history without overwriting the clipboard, and it is not
  applied later either; the sender sees "saved to the peer's history, clipboard
  not overwritten". A paused receive, oversized text or a failed history write
  is never reported as saved.
- Content marked by a password manager is neither synced nor stored.
- Privacy mode in Settings and the tray: while on, local copies are not recorded
  and nothing is sent or received, and the setting survives a restart. Sending
  files on purpose still works. Turning it off resumes from the next copy event
  without replaying what was ignored. Settings → Clipboard sync → Excluded
  source apps takes the full application names shown in history, separated by
  semicolons and case-insensitive: copies made in those apps are neither
  recorded nor synced. An unrecognised source is treated as an ordinary copy,
  and remote source names are not used for exclusion.
- The tray can enable "Ignore the next copy": that copy is neither recorded nor
  synced, and clicking the item again cancels it. It resets after the next local
  copy settles, without touching the system clipboard or deleting past history;
  remote writes and pastes from this app's own history do not consume it. The
  state is not persisted, and pause is not a substitute for it.
- Settings can pause sync, sync text only, change the text and image limits and
  enable launch at login.
- Settings → Clipboard sync switches between automatic and manual. In manual
  mode an ordinary copy only enters local history and incoming content is still
  accepted; "Copy and sync" sends the selected text, image, file or folder.
  Defaults are `⌥⌘C` on macOS and `Ctrl+Alt+C` on Windows; click the shortcut in
  Settings, press the new combination and it is saved, or restore the default.
  A conflict or a failed save keeps the previous shortcut. macOS needs
  Accessibility permission. Nothing is sent when there is no new content, and
  pausing still stops clipboard traffic while sending files keeps working.

## Clipboard history

- History can be searched, pinned, deleted and cleared. Unpinned entries keep
  for 1000 items or 30 days and are also bounded by the content budget, 2 GB by
  default; over the limit the oldest unpinned entries go first. Pinned entries
  count towards the budget but are never deleted automatically; if pinned items
  fill it, the reason is shown instead of silently dropping an entry.
- Long text bodies live in `texts/` inside the data directory and the list keeps
  a short preview; search scans full text in the background. The first launch
  migrates old history automatically, with unchanged copy, pin and cleanup
  behaviour.
- Settings → History storage changes the budget and shows usage and progress.
  The budget covers text, originals, thumbnails, previews and pinned file
  copies; unpinned file entries keep only the path, name and up to 20 preview
  items, without contents, and directories are not measured recursively.
  "Usage breakdown" shows pinned content, the history directory total and,
  computed in the background, the file clipboard cache, the receiving folder and
  damaged history backups; the directory total also covers the index, the change
  log and temporary viewer copies. Closing the window or the section cancels an
  unfinished scan. Received files and backups are never deleted automatically —
  open the folder and tidy it yourself. Refresh updates usage and any expanded
  breakdown, limits apply immediately, and clearing unpinned entries lives on
  the history page behind a second confirmation.
- History changes are appended to `history/history.log`; every 256 operations or
  1 MiB of log produces an `index.json` snapshot. Startup replays the log and
  repairs a torn tail; a complete record that fails validation is reported and
  the file is kept.
- Pasting back from history reads and verifies images in the background so the
  UI thread does not recompute hashes, thumbnails or decode twice. Closing the
  window that started it or deleting the pending entry cancels it.
- List thumbnails are loaded by two background workers with at most 32 queued;
  the cache holds 128 thumbnails within an estimated 8 MiB of bitmap memory and
  is released one minute after leaving history and closing quick history.
- Search highlights matches in the main window and quick history, showing a
  short excerpt for body matches and the field name for matches in notes, source
  apps, device names, file names or directory entries. Every occurrence in the
  excerpt is marked; matches use bold text on a light background, and the
  selected row uses a brighter match with dimmer surrounding text. In quick
  history the right of the search field shows the result count or "Searching…",
  and body text still being read shows the matches of fields already loaded.
  Excerpts are for display only — copy and paste use the full text. Long text
  keeps being read in chunks and closing the window or changing the query
  cancels the old one. Typing an application name seen in history offers
  "Source: app" below the field to filter by it; the candidates are the 24 most
  recent source names and are not read from disk.
- The main window and quick history always show local versus synced plus a
  timestamp, truncating long device and application names with a tooltip for the
  full name. macOS uses the foreground application name, Windows the foreground
  process name, and the peer's history shows the source app too.
- The list uses a separate metadata snapshot, so pinning, deletion and cleanup
  happen in the background; closing the main window does not interrupt cleanup
  or quick history.

## Pinning, notes and backups

- A pinned entry can carry a note or alias of up to 80 characters, shown below
  the entry and persisted with history. Search matches notes and highlights
  them; unpinning clears the note.
- Up to nine pinnable entries are assigned `⌘1`–`⌘9` (`Ctrl+1`–`Ctrl+9` on
  Windows), shown at the end of each row in the main window and quick history,
  and pasting them directly works while quick history is open. The shortcut
  binds to the entry and is saved with history, so it does not follow row order
  or search results; deleting or unpinning frees the shortcut without shifting
  the others. Pinned entries from older versions get available shortcuts after
  an upgrade; beyond nine, pinning and selection still work without numeric
  shortcuts.
- History → Pinned backup exports pinned text, images and notes newest first.
  It contains no device identity, trust records, source information or file
  contents. Archives are not encrypted. Each ZIP holds at most 100000 entries
  and at most 1 GiB of JSON after extraction. Larger backups split automatically:
  the first ZIP uses the chosen filename, with additional `-part-002-*.zip` files
  beside it; the unique suffix prevents overwriting other backups. Select all
  parts together when importing. Each part also works with older importers.
  Import appends entries and reassigns available numeric shortcuts, never
  overwriting existing pins, so importing twice creates
  duplicates. A damaged archive is rejected before writing; a disk or capacity
  error may leave a partial result and the UI reports how many entries were
  processed. Import respects the history budget and may clean unpinned entries.
- A file entry shows whether it is still usable. Pinning a file copies its
  contents into `history/pinned-files/` within the history capacity and entry
  limits, so it stays pasteable after the source is deleted, and the copy is
  removed when unpinned; unpinned entries keep only the path.

## Quick history

- `⌥⌘V` opens quick history on macOS; arrow keys or the mouse select and Enter
  or a double click paste into the field you were in. `Cmd+Shift+V` also works
  by default, and Windows uses `Alt+V`. Settings → Quick history shortcut
  records a new combination by pressing it, applies it immediately and
  remembers it, with a reset to the default. An occupied combination or a
  failed save keeps the previous shortcut, and a custom combination replaces
  the default one.
- The panel starts at 380 × 440. Drag its right, bottom or bottom-right edge to
  resize and the size is remembered; drag empty space (the padding and the gap
  between search field and list) to move it, though the position is not
  remembered.
- Opened by shortcut, the panel stays on the screen of the current foreground
  window: near the pointer, centred horizontally and preferably below it, if
  the pointer is on that screen, otherwise near the input caret, falling back to
  the foreground window when the caret is unreliable. Opened from the tray it
  sits near the icon. It never shrinks below a readable list and never leaves
  the current screen's work area.
- The first paste requires allowing Snapover under System Settings → Privacy &
  Security → Accessibility. Without a remembered window, the content is only
  written to the clipboard and you paste it yourself.
- Both the main window and quick history have a context menu: paste (or copy),
  pin/unpin, set/edit/clear note, delete. In quick history Enter and a double
  click still paste directly; `⌥Enter` (`Alt+Enter` on Windows) only writes to
  the clipboard and closes the panel. `⌘⇧P` (`Ctrl+Shift+P`) toggles the pin and
  `⌘⇧⌫` (`Ctrl+Shift+Backspace`) deletes the current entry, while a plain
  Backspace keeps editing the query. Organising entries keeps the query,
  pinning keeps the entry selected and deleting selects a neighbour. A file
  entry can be copied or pasted while its original path or receive cache is
  intact, and the menu offers reveal in Finder (Explorer on Windows); once the
  path is gone or the cache cleaned, the menu explains that it must be copied
  again.
- At most one paste preparation exists in the whole app; closing the window
  cancels publishing the result and releases what was prepared.

## Previews: images and text

- An image row hovered for 300 ms shows a large preview, to the right by
  default and flipped automatically when space is tight; the pointer can enter
  the preview and it closes after a delay when leaving. The list keeps 160-pixel
  thumbnails, while the hover preview uses a separate image of up to 1120 × 680
  with a 560 × 340 area in the window, balancing Retina sharpness. Only one
  preview is shown and it is released on close, with pixel data including
  reduced levels staying under about 4 MiB. New images reuse the decode done for
  the clipboard and older entries generate previews on demand and cache them to
  disk; originals over 4 megapixels fall back to the small thumbnail so a hover
  never decodes a huge image. The hover card always fits the whole image
  without scrollbars, shrinking with the window and turning very tall images
  into a thin strip.
- "Open larger view", also in the context menu, opens the current image in its
  own window: the wheel steps and a trackpad pinch zoom around the pointer,
  dragging pans, a double click toggles fit-to-window and actual size, the
  toolbar shows the zoom level with ± and two reset buttons, and Esc or Close
  exits. One window exists at a time and a new image replaces the current one;
  decoding is capped at 10 megapixels by downscaling first, and closing the
  window frees the pixels at once. "Open in system app" hands the original to
  the system viewer (Preview on macOS, the default image app on Windows).
  Opening the viewer dismisses the hover preview; from quick history it also
  closes quick history while the main window stays open.
- The original is first copied to `history/opened/` rather than opened in place,
  so a save in the viewer cannot alter the record. An unmodified copy is reused
  and its retention restarted; an edited copy is kept, and reopening generates a
  fresh copy from the history original. The copies total at most 128 MiB and a
  shortage asks you to retry later without deleting a file just opened. Copies
  older than 15 minutes are cleaned every minute, on quit and on the next
  launch. A failed open is only reported; the app does not fall back to an
  embedded scroller. Enter and a double click still paste.
- A truncated text row hovered for 300 ms expands into a preview using MyGo's
  read-only CodeEditor, with line numbers, syntax highlighting for common code
  patterns, wrapping and preserved indentation, scrollable while hovered. Short
  text already fully visible does not open a preview. Hovering reads the first
  16 KiB in the background and notes when more exists; "View full text" opens a
  read-only CodeEditor window with line numbers and highlighting, selectable and
  copyable text and Esc to close. Text over 256 KiB or with very long lines is
  split into segments of at most 16 KiB with paging and "Copy all", without
  changing the original. Full text uses an on-demand window released on close.
  Images do not use this window, and neither images nor text expand on keyboard
  selection alone; search excerpts are display only.
- The paste target follows foreground application and window events and falls
  back to polling only if event registration fails; image previews follow
  pointer enter and leave events and only schedule timers for the hover and
  close delays.

## Interface, motion and feedback

- Page changes, sidebar selection colour, connection state and record counts use
  MyGo's native motion, and device details expand with a short transition. The
  system "Reduce motion" setting is respected, while keyboard selection and
  pasting in quick history stay instant.
- Result toasts carry success, warning or failure icons and colours, and a
  failing toast can be dismissed early instead of looking like success. Sync
  progress is coloured for running, done and failed, ending in a check or cross,
  with a flowing segment when the size is unknown. Paired devices show a
  coloured dot next to the name that pulses while reconnecting, and skeleton
  rows stand in while measuring files and backups or decoding thumbnails so the
  layout does not jump.
- Settings → Language follows the system by default and can be set to Chinese or
  English, switching the interface, menus and update window together; history
  bodies, file names and device names keep their original text.
- Settings → General & Appearance → Appearance offers Follow System, Light and
  Dark. It follows the system by default; changes apply to all windows immediately
  and persist across restarts.
- Launching opens the main window, whose menu and sidebar are the same three
  pages: Settings, Connect devices and Clipboard history. It starts on Connect
  devices, and `-peer` fills the manual address field and starts connecting.
- Windows are created on demand: closing destroys the interface and cancels an
  unfinished settings open, keeping only the "do not ask again" flag. The main
  window is rebuilt with its remembered position and size, while background sync
  and quick history keep running. Launching at login does not create the main
  window, and quitting ends the process.

## Tray, icon and launch at login

- Left-clicking the tray icon on Windows or the menu bar icon on macOS opens an
  options menu (quick history, settings, pause sync, ignore the next copy, quit)
  while right-clicking opens quick history. The menu holds frequent actions
  only, without version or connection state — the tray tooltip shows connection
  state and the sidebar switches pages (the macOS window menu bar keeps only the
  application and edit menus, with "Settings…" in the application menu under
  `⌘,`). The shortcut still opens quick history directly.
- Settings → Menu bar icon on macOS and Settings → Tray icon on Windows offer a
  horizontal preview of the mailbox, bird and envelope icons. The mailbox is the
  default, a change applies immediately and is remembered. The old original icon
  is gone and that choice now means the mailbox, while bird and envelope choices
  are kept. The resources are `internal/resources/icon2.png`,
  `internal/resources/icon1.png` and `internal/resources/icon3.png`; keep their
  alpha channel, and note they are drawn preserving aspect ratio against light
  and dark menu bars and taskbars. Replacing them requires repackaging and
  restarting.
- Launch at login requires the packaged `.app` on macOS; running from source
  cannot register it. Windows writes a startup entry for the current executable.
- With a `portable.flag` next to the executable, data goes to `data/` beside it
  instead of the user profile.

## Sending files on purpose (Windows "Send to" / macOS Finder service)

To copy files or folders to the other computer, just copy and paste as described
above. To drop files straight into the peer's receiving folder without touching
the clipboard, use Windows "Send to", the macOS Finder service or the CLI. Once
both sides run a version with sending and are connected, select one or several
files, or drag them in, and they transfer one after another; ordinary files
including empty ones are supported with no size limit in the app, bounded only
by free disk space and the filesystem. Folders are still unsupported — copy and
paste them instead. Batches up to 20 MiB send silently with a completion
notification; larger ones open a progress window that keeps the result. When no
device is connected or a device needs confirmation, the send window opens so you
can change files and send manually.

The "file copy sync limit" only affects copying files and folders, not sending.
Peers on older versions still validate against their own limit; batches over
20 MiB sent from the send window, plus pause and resume, need both sides
updated.

Files are saved to the peer's receiving folder without changing the clipboard
and without tracking later edits to the original. The default is the system
"Downloads / Snapover" folder, changeable under Settings → File sync →
Receiving folder. Choosing a folder and starting a receive both check that it
can be saved safely, and a filesystem without that support asks for another
folder; a receive already under way keeps writing to the folder it started in.
Name collisions get a numeric suffix instead of overwriting.

The send window shows progress, and success only appears after the peer confirms
the save. Closing the send window keeps the transfer going, with notifications
for completion or failure (when notifications are allowed); the receiver shows
its own notification and can open the folder from Settings → File sync →
Receiving folder. Text and image sync keep working, and sending ignores pause
and text-only mode.

Transfers reuse the trusted device's TLS connection, write chunks to a temporary
file and publish the final file after verification. New receive temporaries live
in a dedicated staging directory, cleaned of leftovers older than 24 hours at
startup and on later receives while active files are kept; scattered temporary
files from older versions cannot be attributed by name alone and are left for
manual review. ZIPs for file clipboard batches live in the application cache,
are removed when sending ends and are reclaimed by cache cleanup after an
abnormal exit. Temporary files from interrupted history text writes are removed
at the next launch once older than 24 hours. Editing the source during a
transfer cancels it. If cancellation cannot confirm whether the peer saved, or
the confirmation times out, check the peer's folder before retrying to avoid
duplicates.

Before receiving, free space and the FAT/FAT32 single-file limit are checked;
space taken during a transfer, write or flush failures report an error, an
unfinished file never appears as final and never overwrites an existing one.
Both sides advance on chunk write confirmations with limited data in flight, so
a slow disk never buffers a whole file in memory, and a single disk operation or
confirmation waits at most 15 minutes without timing out on total transfer time
or low throughput.

Pause, resume and reconnect for large files need both sides on a version with
the feature. Pausing keeps written progress; after a disconnect the app waits
for the same device to reconnect for up to 15 minutes and never diverts to
another device. The receiver keeps up to eight resumable states for 15 minutes
each, verifies the source file is unchanged on reconnect, resumes from the
position the receiver confirmed and still verifies the whole file at the end.
Both apps must stay running; after quitting, send again. Cancelling while
online cleans up unfinished files; cancelling during a disconnect cannot notify
the receiver immediately, so remote temporaries expire. The CLI keeps terminal
progress and Ctrl+C behaviour, times out only after 15 minutes without byte
progress and never opens a window, while file clipboard sync keeps its separate
cache limits.

### Windows "Send to"

The first normal launch adds "Snapover send file" to the current user's "Send
to" menu without administrator rights. Right-click a file → Send to → Snapover
send file to send to the connected device without dragging or a second click;
large files open the progress window while small ones send in the background.
If Windows 11 hides the entry, choose "Show more options" first.

When Snapover is already running the request goes to the existing process
instead of starting a second copy. Otherwise it launches first and tries the
most recent paired device: if that device connects within 10 seconds the file
sends automatically. With no pairing, an offline device or a connection to a
different device, the files stay in the send window with a prompt to connect or
confirm, rather than waiting indefinitely and sending later.

Multiple ordinary files are sent in order; folders or an in-flight transfer are
reported instead of overwriting what is being sent. Settings → File sync →
Receiving folder can disable the entry, and enabling it again or moving the
executable updates the shortcut target on the next launch. The shortcut is
created directly through the Windows Shell Link API without PowerShell; if that
fails the window shows the error and `logs/snapover.log` records the reason.
Copying and pasting files, or the CLI, still work.

The GUI argument works on both Windows and macOS:

```sh
Snapover.exe --send-file "C:\Users\xxx\Documents\report.pdf"
```

### macOS Finder service

With `Snapover.app` in Applications and running, right-click a file → Services →
Snapover send file to send to the connected device, without dragging or a second
click. The entry reuses the running app instead of an Automator workflow, so no
WorkflowServiceRunner appears. Batches up to 20 MiB send in the background with
a completion notification once the peer confirms the save; larger ones open the
progress window with pause, resume and cancel, keeping a textual result when
done. The receiver saves entirely in the background and notifies on completion
(when notifications are allowed).

When the app is running the request goes to the existing process; otherwise it
launches in the background and tries the most recent paired device, sending
automatically if that device connects within 10 seconds. With no paired device,
a timeout or a connection to another device, the files are kept and the send
window opens with a prompt to connect or confirm. As on Windows, several
ordinary files are supported, and folders or a second request while sending are
reported.

Settings → File sync → Receiving folder can disable the service or "Refresh
Finder service". macOS owns the menu: if the entry is missing or should be
hidden, search for "Services" in System Settings and adjust "Snapover send
file", noting the location differs between macOS versions. Running from source
with `go run` does not register the entry — use a packaged `Snapover.app`. A new
version removes the old Automator quick action; idle runners the system kept are
reclaimed by macOS.

The app's own executable can also be called directly:

```sh
/Applications/Snapover.app/Contents/MacOS/Snapover --send-file "/Users/xxx/Documents/report.pdf"
```

## Tailscale across networks

1. Install and connect Tailscale on both computers, and let them reach
   Snapover's inbound TCP port, `47821` by default — use your custom port if you
   changed it.
2. On both sides open Snapover → Settings → This device & diagnostics and enable
   "Allow connections over Tailscale", off by default.
3. When adding for the first time, press "Add device" on both sides. The
   initiating side expands "Connect by address", chooses "Tailscale" and enters
   the other computer's Tailscale IPv4 address
   (`100.64.0.0–100.127.255.255`), with the port optional. Compare the matching
   code and confirm on each side.
4. For an already paired device, open its details and press "Add Tailscale
   address" or "Edit Tailscale address", enter the address, then "Verify and
   connect". The address is saved only after the device ID, certificate and
   pairing all verify; a failure keeps the previous address.

A device stores its LAN and Tailscale addresses separately. Automatic
reconnection tries the currently discovered LAN address, the saved LAN address
and the saved Tailscale address, de-duplicated to at most three candidates; an
address you specified explicitly is tried first. Each failed round backs off by
1, 2, 4 seconds, up to 30 seconds. An established connection stays, so a
returning LAN link does not preempt a Tailscale session; the next disconnect
picks the path again. Candidates with the wrong device or fingerprint are
disabled until the address is edited or you reconnect manually.

Disabling the switch ends related Tailscale connections and dials, keeps pairing
and addresses, and keeps trying the device over LAN; LAN sessions are unaffected.
Deleting a Tailscale address does not unpair. A file send is bound to the device
identity, so a path change never redirects it and an interruption is reported as
usual.

The first version uses Tailscale through the system's connected IPv4 tunnel. It
does not call the management API, does not discover devices across networks and
adds no MagicDNS, Tailscale IPv6 or subnet routing. The macOS `utun` interface
and the Windows Tailscale interface need a local IPv4 address within the shared
range; without a usable tunnel the app does not fall back to the physical
interface. Multiple indistinguishable tunnels in the shared range are refused
with a note to check other VPNs. Address ranges and interfaces only choose the
path — trust still comes from Snapover's TLS pairing.

"This device & diagnostics → Device info" shows the local Tailscale IPv4 address.
The connection test follows the chosen path but only checks the TCP port, not
TLS or pairing. When troubleshooting, check both sides' switches, Tailscale
online state, addresses and ports, device access rules and firewalls; a timeout
alone does not identify which one is at fault. Using `-listen` with a specific
address never widens the listening range — listen on the Tailscale address or
use `-listen :47821`. `-peer` reuses the saved path when it matches a stored
Tailscale address; new Tailscale addresses are added from the GUI first.

Older pairing records load directly. The new format adds optional `lan_addr` and
`tailscale_addr` while keeping the compatible `addr` field; after a downgrade an
older version may not use the most recent Tailscale address, and saving again
may drop the newer fields, so dual addresses are not guaranteed across
downgrades.

The real-device acceptance steps for cross-network use are in the Tailscale plan
(Chinese: [tailscale-plan.md](tailscale-plan.md#9-实机验收与完成条件)). Automated
tests need no real Tailscale but cannot replace two-machine testing on Windows
and macOS, with different Tailscale versions and over slow or relayed links.

## Data directory, logs and diagnostics

Data directory:

- macOS: `~/Library/Application Support/Snapover`
- Windows: `%AppData%\Snapover`

It holds the machine certificate and `peers.json`. Never copy `identity/` to
another computer.

Logs are in `logs/` beside it. `snapover.log` and `crash.log` rotate at about
1 MB keeping one previous file; entries older than seven days and surplus
backups are removed at startup and while running. The active `session.open` is
never removed.

- `snapover.log`: the run log.
- `crash.log`: crash records. Go panics and fatal runtime errors write their
  stack. After a crash on copy, the last line is usually the clipboard format
  being read, not its content. If the previous exit was unclean, a line is
  written at the next launch. A file without a stack means a native crash: on
  macOS look for reports starting with Snapover in `~/Library/Logs/DiagnosticReports`,
  on Windows use Event Viewer.

Diagnostic breadcrumbs go through a bounded queue written mostly once per second
and rotated while running; quitting and explicit blocking stacks wait for the
flush. Under heavy blocking a full queue drops ordinary diagnostic lines, so the
last breadcrumb is not an exact crash position. Fatal runtime errors still write
straight to the crash file.

Settings → This device & diagnostics → Connection diagnostics generates, shows
and copies a summary with platform, connection state, sync policy and recent
result categories — no clipboard content, paths, device names, addresses or
certificates. Copying refreshes the summary. The TCP test still only checks port
reachability.

At most 16 file notifications are queued and 16 kept.

## Windows keep-awake

Settings → Keep awake offers "Prevent automatic screen lock", off by default.
When on, the app asks the system and display to stay awake and, after about 60
seconds idle, tries one zero-displacement mouse input to reset the screensaver
timer, producing no key presses or clicks. Closing the window keeps it running
in the tray; locking the screen manually or leaving the interactive desktop
pauses it until you return. Turning the switch off or quitting stops it. If the
system forbids simulated input for the screensaver timer, permissions are
insufficient or the call fails, the reason is shown and background requests
stop; domain policies, the registry and power plans are never modified. Verify
the effect on a real Windows machine — it costs battery, and the steps are in
[keep-awake-validation.md](keep-awake-validation.md) (Chinese).

## Resource and memory bounds

- Image decoding, thumbnail and preview limits are described above; after a run
  of large images the app waits for idle instead of forcing a GC per image.
- History uses a separate metadata snapshot, so pinning, deletion and cleanup
  run in the background and closing the main window does not interrupt them.
- At most one paste preparation exists app-wide; closing the window cancels
  publishing and releases it.
- At most 16 notifications are queued and 16 kept.
- Diagnostic breadcrumbs use a bounded queue that drops ordinary lines when full.

Fixes and measurements are recorded in the performance notes (Chinese:
[performance-2026-10-16.md](performance-2026-10-16.md)).

## Pairing and connection internals

- Self-signed TLS 1.3. Pairing happens through a code bound to the handshake plus
  explicit confirmation on both sides; afterwards the certificate fingerprint is
  pinned, and unpairing closes the connection immediately.
- When a peer connects over the LAN, its source IP and listening port replace the
  saved address. Connections from virtual interfaces are dropped by default and
  LAN dials bind to the physical interface, so a VPN address never becomes the
  address used next time.
- Discovery and connection ignore virtual interfaces by default; Tailscale IPv4
  addresses can be allowed separately and never take part in LAN broadcast. The
  subnet list under Connect devices narrows broadcast; unset means all physical
  subnets. Where broadcast is isolated, blocked by a VPN or by system
  permissions, enter the address by hand.
- Connect devices lets you choose which subnets take part in broadcast and
  discovery, remembered by interface name and subnet without the current IP.
  Unset or "Select all" uses every physical subnet, including ones that appear
  later; after narrowing, new subnets stay out. Clearing everything stops
  automatic discovery while manual addresses still work.
- Both sides listen, so one reachable direction is enough to connect. Switching
  devices mid-send or a disconnect still fails rather than redirecting the
  transfer.

### Command-line flags

```sh
# Start with a peer address (port 47821 is assumed when omitted)
go run ./cmd/snapover -peer 192.168.1.20:47821

# Inspect clipboard type and size without connecting or printing its contents
go run ./cmd/snapover -check

# Restrict the listening address; this never widens the range, and Tailscale
# setups should use -listen :47821
go run ./cmd/snapover -listen :47821

# Clear every pairing record
go run ./cmd/snapover -reset-peers
```

`-check` writes only clipboard metadata to the console and does not open or
maintain the app's history, identity, settings or log files. It can run while the
background app is active. Other startup flags do not take effect in this mode.

The startup log prints the local LAN address and the certificate fingerprint.

### Connection help and system permissions

- Windows Firewall must allow inbound TCP 47821, and discovery needs UDP 5353.
  macOS asks for Local Network access the first time; the package declares its
  purpose and the Bonjour service. When discovery fails the device page shows
  recovery steps, and System Settings → Privacy & Security → Local Network lists
  the permission.
- Before pairing, the Windows Connect devices page offers a firewall shortcut;
  if the firewall prompt appears after the listener starts, allow it. When the
  prompt does not appear, "Open firewall settings" ticks Snapover on private
  networks. Detailed steps stay in connection help: go back and search again,
  and expand troubleshooting only after about 10 seconds of nothing. Dismissing
  the reminder or connecting successfully stops the prompt, and connection help
  can reopen it later.
- On macOS 15 and later, before pairing, Connect devices explains what Local
  Network is for and starting discovery triggers the prompt — choose Allow. If
  the prompt does not appear, use "Open Local Network settings", and when only
  Privacy & Security opened, pick Local Network manually. Connection help still
  offers a new search and recovery steps. macOS 13 and 14 have no Local Network
  entry and keep ordinary connection help. Accessibility permission is still
  requested only when quick paste is first used.
