package middleware

import (
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// blockedPaths are exact paths that vulnerability scanners probe.
var blockedPaths = map[string]struct{}{
	"/.env":            {},
	"/.env.prod":       {},
	"/.env.production": {},
	"/.env.local":      {},
	"/.env.backup":     {},
	"/.env.dev":        {},
	"/.env.staging":    {},
	"/.git/config":     {},
	"/.git/head":       {},
	"/.aws/credentials": {},
	"/wp-login.php":    {},
	"/wp-admin":        {},
	"/xmlrpc.php":      {},
	"/phpmyadmin":      {},
	"/phpinfo.php":     {},
	"/.ds_store":       {},
	"/server-status":   {},
	"/actuator":        {},
	"/actuator/env":    {},
	"/debug/vars":      {},
	"/debug/pprof":     {},
}

// blockedPrefixes are path prefixes commonly probed by scanners.
var blockedPrefixes = []string{
	"/.env",
	"/.git/",
	"/.svn/",
	"/.hg/",
	"/wp-",
	"/cgi-bin/",
	"/vendor/phpunit/",
}

// blockedSuffixes catch nested .env probes like /app/.env, /laravel/.env.
var blockedSuffixes = []string{
	"/.env",
	"/env.txt",
	"/env.js",
	"/env.ts",
	"/env.py",
	"/env.sh",
	"/env.bash",
	"/env.json",
	"/env.yaml",
	"/env.yml",
	"/env.example",
	"/env.template",
	"/env.md",
	"/env.production",
	"/env.development",
}

// blockedExtensions are file extensions that should never be served by an API.
// Only match when the extension is the final segment (not a substring).
var blockedExtensions = []string{
	".php",
	".asp",
	".aspx",
	".jsp",
	".cgi",
	".bak",
	".sql",
}

// ScannerBlock silently drops requests to paths that only vulnerability
// scanners and bots would hit. Returns 404 with no body to waste minimal
// resources and avoid polluting error alerting.
//
// Place this AFTER Recovery (so panics are caught) and BEFORE Logger/ErrorAlerter
// (so scanner noise doesn't reach alerting).
func ScannerBlock(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// path.Clean normalizes /./foo, //foo, /../foo etc.
		cleaned := path.Clean(c.Request.URL.Path)

		if isScannerPath(cleaned) {
			logger.Debug("scanner probe blocked",
				zap.String("path", c.Request.URL.Path),
				zap.String("ip", c.ClientIP()),
			)
			c.AbortWithStatus(http.StatusNotFound)
			return
		}

		c.Next()
	}
}

func isScannerPath(p string) bool {
	lower := strings.ToLower(p)

	if _, ok := blockedPaths[lower]; ok {
		return true
	}

	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	for _, suffix := range blockedSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	// Block any path containing /.env (catches /app/.env, /src/.env, etc.)
	if strings.Contains(lower, "/.env") {
		return true
	}

	// Block bare /env* paths (env.txt, env.py, env.sh, etc.) at root
	if strings.HasPrefix(lower, "/env") && !strings.HasPrefix(lower, "/environment") {
		// Allow actual API paths that might start with /env (unlikely but safe)
		if len(lower) > 4 && (lower[4] == '.' || lower[4] == '_' || lower[4] == '-') {
			return true
		}
	}

	// Block /phpinfo, /_profiler, /_environment
	if strings.HasPrefix(lower, "/phpinfo") || strings.HasPrefix(lower, "/_profiler") || strings.HasPrefix(lower, "/_environment") {
		return true
	}

	// Only match extensions on the last path segment
	lastSlash := strings.LastIndex(lower, "/")
	segment := lower
	if lastSlash >= 0 {
		segment = lower[lastSlash:]
	}
	for _, ext := range blockedExtensions {
		if strings.HasSuffix(segment, ext) {
			return true
		}
	}

	return false
}
