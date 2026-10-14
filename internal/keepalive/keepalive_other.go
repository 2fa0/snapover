//go:build !windows

package keepalive

const Supported = false

func currentState() (sessionState, error) { return sessionState{}, nil }
func moveMouse() error                    { return nil }
