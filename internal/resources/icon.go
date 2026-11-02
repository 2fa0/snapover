// Package resources embeds the tray icon.
package resources

import _ "embed"

//go:embed icon1.png
var Icon1PNG []byte

//go:embed icon2.png
var Icon2PNG []byte

//go:embed icon3.png
var Icon3PNG []byte
