package main

import (
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Limits against filling the disk or flooding the service. They can be
// changed through environment variables, see loadConfig.
var (
	maxUploadBytes  int64 = 10 << 20 // per uploaded image
	maxArticleBytes int64 = 1 << 20  // per submitted article
	storageQuota    int64 = 1 << 30  // articles.json plus all uploads
	rateLimit             = 120      // POST requests per client and minute, 0 = off
	trustProxy            = false    // take the client IP from X-Forwarded-For
)

// imageTypes maps the accepted upload types, detected from the file content,
// to the extension the file is stored with. SVG is left out on purpose
// because it can contain scripts.
var imageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

func isTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}

// storageUsed returns the bytes taken by articles.json and all uploads.
func storageUsed() int64 {
	var total int64
	if info, err := os.Stat(filePath); err == nil {
		total += info.Size()
	}
	filepath.WalkDir(uploadDir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// clientIP identifies the client for rate limiting. Behind a reverse proxy
// all requests come from the proxy, so with trustProxy the last address in
// X-Forwarded-For, the one added by the proxy, is used instead.
func clientIP(r *http.Request) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// rateLimiter counts requests per client in fixed one-minute windows.
type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	clients map[string]*rateWindow
}

type rateWindow struct {
	start time.Time
	count int
}

const rateWindowLength = time.Minute

func newRateLimiter(limit int) *rateLimiter {
	return &rateLimiter{limit: limit, clients: make(map[string]*rateWindow)}
}

func (l *rateLimiter) allow(client string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.clients[client]
	if !ok || now.Sub(w.start) >= rateWindowLength {
		// Drop expired windows now and then so the map cannot grow forever.
		if len(l.clients) >= 10000 {
			for c, old := range l.clients {
				if now.Sub(old.start) >= rateWindowLength {
					delete(l.clients, c)
				}
			}
		}
		w = &rateWindow{start: now}
		l.clients[client] = w
	}
	w.count++
	return w.count <= l.limit
}

// limitWrites applies the rate limit to POST requests; reading stays free.
func (l *rateLimiter) limitWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if l.limit > 0 && r.Method == http.MethodPost && !l.allow(clientIP(r)) {
			LogRequest(r).WithField("client", clientIP(r)).Warn("Rate limit exceeded")
			http.Error(w, "Too many requests", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
