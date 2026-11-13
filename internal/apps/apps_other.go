//go:build !darwin && !windows

package apps

import "errors"

// running finds no applications here: the settings page still takes the apps
// it is told about by name.
func running() []App { return nil }

func describe(path string) (App, error) {
	return App{}, errors.New("当前平台无法读取应用程序")
}
