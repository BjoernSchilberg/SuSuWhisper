package main

import (
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sirupsen/logrus"
)

var log = logrus.New()

func initLogger() {
	// Create logs directory if it doesn't exist
	logsDir := "logs"
	if err := os.MkdirAll(logsDir, 0755); err != nil {
		log.Fatal("Failed to create logs directory:", err)
	}

	// Set up log file
	logFile := filepath.Join(logsDir, "susuwhisper.log")
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal("Failed to open log file:", err)
	}

	// Configure logger
	log.SetOutput(file)
	log.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: time.RFC3339,
	})
	log.SetLevel(logrus.InfoLevel)

	// Add some default fields
	log.WithFields(logrus.Fields{
		"service": "susuwhisper",
		"version": "1.0.0",
	}).Info("Logger initialized")
}

// LogRequest creates a new logger with request-specific fields
func LogRequest(r *http.Request) *logrus.Entry {
	return log.WithFields(logrus.Fields{
		"method":      r.Method,
		"path":        r.URL.Path,
		"remote_addr": r.RemoteAddr,
		"user_agent":  r.UserAgent(),
	})
}
