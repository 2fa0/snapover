package keepawake

import "errors"

var errSessionUnavailable = errors.New("会话查询服务不可用")

// A failed WTS service query may fall back to the physical console only.
// Callers must still verify that their desktop currently receives input.
func activeSession(query, console func() (bool, error)) (bool, error) {
	active, err := query()
	if errors.Is(err, errSessionUnavailable) {
		return console()
	}
	return active, err
}
