package security

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"
)

const (
	hibpAPIURL     = "https://api.pwnedpasswords.com/range/"
	minLength      = 8
	maxLength      = 128
	hibpTimeout    = 5 * time.Second
)

type PasswordPolicyService struct {
	httpClient *http.Client
	checkBreaches bool
}

type PasswordValidationResult struct {
	Valid       bool
	Errors      []string
	Warnings    []string
	Strength    int // 0-100
	IsBreached  bool
	BreachCount int
}

func NewPasswordPolicyService(checkBreaches bool) *PasswordPolicyService {
	return &PasswordPolicyService{
		httpClient: &http.Client{Timeout: hibpTimeout},
		checkBreaches: checkBreaches,
	}
}

// ValidatePassword validates password against policy and optionally checks breaches
func (s *PasswordPolicyService) ValidatePassword(ctx context.Context, password string) (*PasswordValidationResult, error) {
	result := &PasswordValidationResult{
		Valid:    true,
		Errors:   []string{},
		Warnings: []string{},
	}

	// Length check
	if len(password) < minLength {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("Password must be at least %d characters", minLength))
	}
	if len(password) > maxLength {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("Password must be at most %d characters", maxLength))
	}

	// Complexity checks
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	for _, c := range password {
		switch {
		case unicode.IsUpper(c):
			hasUpper = true
		case unicode.IsLower(c):
			hasLower = true
		case unicode.IsDigit(c):
			hasDigit = true
		case unicode.IsPunct(c) || unicode.IsSymbol(c):
			hasSpecial = true
		}
	}

	complexityCount := 0
	if hasUpper {
		complexityCount++
	}
	if hasLower {
		complexityCount++
	}
	if hasDigit {
		complexityCount++
	}
	if hasSpecial {
		complexityCount++
	}

	if complexityCount < 3 {
		result.Valid = false
		result.Errors = append(result.Errors, "Password must contain at least 3 of: uppercase, lowercase, digit, special character")
	}

	// Common password patterns
	lowerPass := strings.ToLower(password)
	commonPatterns := []string{"password", "123456", "qwerty", "admin", "letmein", "welcome", "monkey", "dragon"}
	for _, pattern := range commonPatterns {
		if strings.Contains(lowerPass, pattern) {
			result.Valid = false
			result.Errors = append(result.Errors, "Password contains common pattern")
			break
		}
	}

	// Sequential characters check
	if hasSequentialChars(password, 4) {
		result.Warnings = append(result.Warnings, "Password contains sequential characters")
	}

	// Repeated characters check
	if hasRepeatedChars(password, 3) {
		result.Warnings = append(result.Warnings, "Password contains repeated characters")
	}

	// Calculate strength score
	result.Strength = calculateStrength(password, hasUpper, hasLower, hasDigit, hasSpecial)

	// Check HaveIBeenPwned if enabled and password passes basic validation
	if s.checkBreaches && result.Valid {
		breached, count, err := s.checkPasswordBreach(ctx, password)
		if err == nil && breached {
			result.IsBreached = true
			result.BreachCount = count
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("Password found in %d data breaches - choose a different password", count))
		}
	}

	return result, nil
}

// checkPasswordBreach checks password against HaveIBeenPwned API using k-anonymity
func (s *PasswordPolicyService) checkPasswordBreach(ctx context.Context, password string) (bool, int, error) {
	// SHA1 hash the password
	hash := sha1.Sum([]byte(password))
	hashStr := strings.ToUpper(hex.EncodeToString(hash[:]))

	// Send only first 5 characters (k-anonymity)
	prefix := hashStr[:5]
	suffix := hashStr[5:]

	req, err := http.NewRequestWithContext(ctx, "GET", hibpAPIURL+prefix, nil)
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("User-Agent", "RAIL-Security-Check")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, 0, fmt.Errorf("HIBP API returned status %d", resp.StatusCode)
	}

	// Read response and check for our suffix
	buf := make([]byte, 64*1024)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	for _, line := range strings.Split(body, "\r\n") {
		parts := strings.Split(line, ":")
		if len(parts) == 2 && parts[0] == suffix {
			var count int
			fmt.Sscanf(parts[1], "%d", &count)
			return true, count, nil
		}
	}

	return false, 0, nil
}

func hasSequentialChars(s string, minSeq int) bool {
	if len(s) < minSeq {
		return false
	}
	count := 1
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1]+1 || s[i] == s[i-1]-1 {
			count++
			if count >= minSeq {
				return true
			}
		} else {
			count = 1
		}
	}
	return false
}

func hasRepeatedChars(s string, minRepeat int) bool {
	if len(s) < minRepeat {
		return false
	}
	count := 1
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			count++
			if count >= minRepeat {
				return true
			}
		} else {
			count = 1
		}
	}
	return false
}

func calculateStrength(password string, hasUpper, hasLower, hasDigit, hasSpecial bool) int {
	score := 0

	// Length contribution (up to 40 points)
	length := len(password)
	if length >= 8 {
		score += 20
	}
	if length >= 12 {
		score += 10
	}
	if length >= 16 {
		score += 10
	}

	// Complexity contribution (up to 40 points)
	if hasUpper {
		score += 10
	}
	if hasLower {
		score += 10
	}
	if hasDigit {
		score += 10
	}
	if hasSpecial {
		score += 10
	}

	// Variety contribution (up to 20 points)
	uniqueChars := make(map[rune]bool)
	for _, c := range password {
		uniqueChars[c] = true
	}
	uniqueRatio := float64(len(uniqueChars)) / float64(length)
	score += int(uniqueRatio * 20)

	if score > 100 {
		score = 100
	}
	return score
}
