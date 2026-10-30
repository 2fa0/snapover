// Package resources embeds the tray icon.
package resources

import _ "embed"

// IconPNG is the transparent bird used for the menu bar and tray icons.
//
//go:embed icon.png
var IconPNG []byte

//go:embed icon1.png
var Icon1PNG []byte

//go:embed icon2.png
var Icon2PNG []byte

//go:embed icon3.png
var Icon3PNG []byte
