package metrics

import "errors"

// ErrNotFound means no metric of that kind and name is stored.
var ErrNotFound = errors.New("metric not found")
