//go:build !darwin && !windows

package front

func CurrentSource() Source { return Source{} }
