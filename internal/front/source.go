package front

// Source is the foreground app that owned a copy.
type Source struct {
	// Name is the app's display name, which is what history and the settings
	// list show.
	Name string
	// ID tells the app apart from another with the same name: the bundle
	// identifier on macOS, the executable path on Windows. The settings list
	// matches it before the name, so renaming the app keeps its rule. Empty
	// when the platform cannot tell.
	ID string
}

// Current is the display name of the foreground app.
// An empty string means unknown.
func Current() string {
	return CurrentSource().Name
}
