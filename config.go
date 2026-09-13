package main

import (
	"fmt"
	"os"
	"strconv"
)

// defaultAddr only accepts connections from the same machine. The service
// is meant to run behind a reverse proxy such as Caddy; in a container set
// SUSU_ADDR=:8080 and publish the port on 127.0.0.1 only.
const defaultAddr = "127.0.0.1:8080"

// loadConfig reads the settings from the environment and returns the
// address to listen on. Empty variables keep their defaults.
//
//	SUSU_ADDR              listen address (default 127.0.0.1:8080)
//	SUSU_STORAGE_QUOTA_MB  space for articles and uploads (default 1024)
//	SUSU_RATE_LIMIT        POST requests per client and minute, 0 = off (default 120)
//	SUSU_TRUST_PROXY       true behind a reverse proxy that sets X-Forwarded-For
func loadConfig() (string, error) {
	addr := defaultAddr
	if v := os.Getenv("SUSU_ADDR"); v != "" {
		addr = v
	}

	if v := os.Getenv("SUSU_STORAGE_QUOTA_MB"); v != "" {
		mb, err := strconv.ParseInt(v, 10, 64)
		if err != nil || mb <= 0 {
			return "", fmt.Errorf("SUSU_STORAGE_QUOTA_MB=%q: want a positive number", v)
		}
		storageQuota = mb << 20
	}

	if v := os.Getenv("SUSU_RATE_LIMIT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return "", fmt.Errorf("SUSU_RATE_LIMIT=%q: want 0 or a positive number", v)
		}
		rateLimit = n
	}

	if v := os.Getenv("SUSU_TRUST_PROXY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return "", fmt.Errorf("SUSU_TRUST_PROXY=%q: want true or false", v)
		}
		trustProxy = b
	}

	return addr, nil
}
