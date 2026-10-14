//go:build !windows && !darwin

package main

import "github.com/egoist/mygo/ui"

func (a *app) initSendTo()                  {}
func (a *app) sendToSettings(c *ui.Context) {}
