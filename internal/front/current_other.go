//go:build !darwin && !windows

package front

func Current() string { return "" }
