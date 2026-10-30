package resources

import _ "embed"

// LogoPNG is the bird shown in the sidebar header, on a transparent
// background.
//
// It is bird.png, the artwork the app icon and tray icon are drawn from,
// cropped to the bird and scaled to 192 pixels: sharp up to 3× at 64 DIPs, and
// small enough to stay out of the binary's way at 31 KiB. Regenerate it from
// bird.png rather than editing it by hand.
//
//go:embed logo.png
var LogoPNG []byte
