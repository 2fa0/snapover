// Package apps names the applications a copy can come from, so the settings
// list is chosen from what is running instead of typed by hand.
package apps

// App is one application the settings list can hold.
type App struct {
	// Name is the name the system shows for the app.
	Name string
	// ID is what tells two apps of one name apart: the bundle identifier on
	// macOS, the executable path on Windows. Empty when unknown.
	ID string
	// Icon is the app's icon as a PNG, or nil when the platform offers none.
	Icon []byte
}

// iconPixels bounds an icon: the list draws it at 20 points, so this stays
// sharp on a 3x display without keeping a megabyte of pixels per app.
const iconPixels = 64

// Running lists the apps that have a user interface right now, without this
// one. The order is the system's. It may take a while on first use, so the
// settings page calls it off its UI thread and keeps what it returns.
func Running() []App { return running() }

// Describe names the app bundle or executable at path.
func Describe(path string) (App, error) { return describe(path) }
