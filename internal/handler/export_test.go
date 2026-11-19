package handler

import (
	"log/slog"
	"net/http"
	"time"
)

func NewRouterWithClock(s Storage, db Pinger, lg *slog.Logger, key []byte, now func() time.Time) http.Handler {
	return newRouter(s, db, lg, key, now)
}

func Verified(r *http.Request) bool {
	return verified(r)
}
