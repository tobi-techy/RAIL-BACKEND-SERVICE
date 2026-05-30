package middleware

import (
	"path"
	"testing"
)

func TestIsScannerPath(t *testing.T) {
	blocked := []string{
		"/.env", "/.env.prod", "/.env.production", "/.env.local",
		"/.git/config", "/.git/HEAD", "/.git/objects/pack/info",
		"/wp-login.php", "/wp-admin", "/xmlrpc.php",
		"/phpmyadmin", "/phpinfo.php",
		"/backup.sql", "/shell.php",
		"/cgi-bin/test", "/vendor/phpunit/src",
		"/.svn/entries", "/.hg/store",
		"/.ENV", "/.Env.Prod", // case-insensitive
		"/.env.dev", "/.env.staging",
	}

	for _, p := range blocked {
		cleaned := path.Clean(p)
		if !isScannerPath(cleaned) {
			t.Errorf("expected %q to be blocked", p)
		}
	}

	allowed := []string{
		"/health", "/api/v1/auth/login", "/api/v1/users/me",
		"/swagger/index.html", "/ping", "/ready",
		"/api/v1/deposits", "/api/v1/withdrawals",
		"/api/v1/activity-log",       // should NOT match .log extension
		"/api/v1/admin/users",        // /admin removed from exact list
		"/.well-known/security.txt",  // legitimate RFC 9116
	}

	for _, p := range allowed {
		cleaned := path.Clean(p)
		if isScannerPath(cleaned) {
			t.Errorf("expected %q to be allowed", p)
		}
	}
}

func TestIsScannerPath_Normalization(t *testing.T) {
	// Attackers try path traversal to bypass filters
	bypasses := []string{
		"///.env",
		"/./env/../.env",
		"/.env/.",
	}

	for _, p := range bypasses {
		cleaned := path.Clean(p)
		if !isScannerPath(cleaned) {
			t.Errorf("expected normalized %q (from %q) to be blocked", cleaned, p)
		}
	}
}
