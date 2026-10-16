package handler

import (
	"compress/gzip"
	"fmt"
	"mime"
	"net/http"
	"strings"
)

type gzipResponseWriter struct {
	w       http.ResponseWriter
	gz      *gzip.Writer
	decided bool
}

var _ http.ResponseWriter = (*gzipResponseWriter)(nil)

func (gw *gzipResponseWriter) Header() http.Header {
	return gw.w.Header()
}

func (gw *gzipResponseWriter) Write(b []byte) (int, error) {
	if !gw.decided {
		gw.WriteHeader(http.StatusOK)
	}
	if gw.gz != nil {
		return gw.gz.Write(b)
	}
	return gw.w.Write(b)
}

// WriteHeader decides whether to compress: the handler sets Content-Type after the middleware
// has run, so this is the first moment the type is known.
func (gw *gzipResponseWriter) WriteHeader(code int) {
	if !gw.decided {
		gw.decided = true
		if compressible(gw.w.Header().Get("Content-Type")) {
			gw.w.Header().Set("Content-Encoding", "gzip")
			// The handler's length counts the uncompressed body.
			gw.w.Header().Del("Content-Length")
			gw.gz = gzip.NewWriter(gw.w)
		}
	}
	gw.w.WriteHeader(code)
}

// WithGzip checks the request encoding first, so a rejection never goes through the gzip writer.
func WithGzip(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch ce := r.Header.Get("Content-Encoding"); ce {
		case "", "identity":
		case "gzip":
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, "body is not valid gzip", http.StatusBadRequest)
				return
			}
			defer zr.Close()
			r.Body = zr
		default:
			w.Header().Set("Accept-Encoding", "gzip")
			http.Error(w, fmt.Sprintf("unsupported Content-Encoding: %s", ce), http.StatusUnsupportedMediaType)
			return
		}

		if !acceptsGzip(r.Header) {
			h.ServeHTTP(w, r)
			return
		}

		gw := &gzipResponseWriter{w: w}
		h.ServeHTTP(gw, r)
		if gw.gz != nil {
			// Close flushes the tail; the headers are already sent, so a failure has nobody to tell.
			_ = gw.gz.Close()
		}
	})
}

func acceptsGzip(h http.Header) bool {
	for _, line := range h.Values("Accept-Encoding") {
		for token := range strings.SplitSeq(line, ",") {
			name, _, _ := strings.Cut(token, ";")
			if strings.EqualFold(strings.TrimSpace(name), "gzip") {
				return true
			}
		}
	}
	return false
}

func compressible(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || mediaType == "text/html"
}
