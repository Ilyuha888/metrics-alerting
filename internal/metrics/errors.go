package metrics

import "errors"

// ErrNotFound means no metric of that kind and name is stored; returned unwrapped, so == works.
var ErrNotFound = errors.New("metric not found")
