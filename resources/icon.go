// Package resources embeds the tray icon.
package resources

import _ "embed"

// IconPNG is the transparent bird used for the menu bar and tray icons.
//
//go:embed icon.png
var IconPNG []byte
