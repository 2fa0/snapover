//go:build !windows && !darwin

package main

import (
	"context"
	"errors"
)

func detectNetworkGuideKind() networkGuideKind { return guideUnsupported }

func openNetworkGuideSettings(context.Context, bool) error {
	return errors.New("network settings are unavailable on this platform")
}
