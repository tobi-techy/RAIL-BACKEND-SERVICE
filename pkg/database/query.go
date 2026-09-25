package database

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

const DefaultQueryTimeout = 30 * time.Second

// WithQueryTimeout returns a context with the specified timeout.
// This ensures queries don't run longer than the configured threshold.
func WithQueryTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = DefaultQueryTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// WithDefaultQueryTimeout returns a context with the default query timeout (30s).
// Use this for database operations that should have a bounded execution time.
func WithDefaultQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithQueryTimeout(ctx, DefaultQueryTimeout)
}

// columnNamePattern allows only safe SQL identifiers (optionally table- or
// schema-qualified, e.g. "user_id", "users.id", "public.users.id").
// Column names are interpolated into the query string, so anything else is
// rejected to prevent SQL injection via map keys. Quoted identifiers, JSON
// operators, function calls, and whitespace are intentionally not supported.
// If you need those, add an explicit allowlist mapping — never concat raw input.
var columnNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// BuildWhereClause builds a parameterized WHERE clause from conditions.
// Keys must be safe SQL identifiers (see columnNamePattern); values are always
// passed as query arguments and never interpolated.
//
// Fail-loud: if ANY key is rejected, the whole clause fails with an error and
// returns "", nil, err. Callers must not fall back to an unfiltered query —
// treating "no valid keys" as "no filter" would expose the entire table.
// An explicitly empty conditions map returns "", nil, nil.
func BuildWhereClause(conditions map[string]interface{}) (string, []interface{}, error) {
	if len(conditions) == 0 {
		return "", nil, nil
	}

	// Sort keys so placeholder numbering ($1, $2, ...) is deterministic.
	keys := make([]string, 0, len(conditions))
	for key := range conditions {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var invalid []string
	for _, key := range keys {
		if !columnNamePattern.MatchString(key) {
			invalid = append(invalid, key)
		}
	}
	if len(invalid) > 0 {
		return "", nil, fmt.Errorf("invalid column name(s) in WHERE clause: %s", strings.Join(invalid, ", "))
	}

	var clauses []string
	var args []interface{}
	for i, key := range keys {
		clauses = append(clauses, fmt.Sprintf("%s = $%d", key, i+1))
		args = append(args, conditions[key])
	}

	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}

// BuildOrderByClause builds an ORDER BY clause allowlisted against
// allowedColumns. Fail-silent by design for backward compatibility: an empty
// or non-allowlisted input returns "" (no ordering) rather than an error.
//
// WARNING: do not mistake "" for success — it means "no ordering applied".
// If you need to distinguish "caller asked for an invalid column" from "no
// ordering requested", use BuildOrderByClauseStrict which returns an error.
func BuildOrderByClause(orderBy string, allowedColumns []string) string {
	clause, err := BuildOrderByClauseStrict(orderBy, allowedColumns)
	if err != nil {
		return ""
	}
	return clause
}

// BuildOrderByClauseStrict is the fail-loud variant: empty input returns
// ("", nil); a non-allowlisted column returns ("", error) so callers can
// never mistake a rejected sort for "no ordering".
func BuildOrderByClauseStrict(orderBy string, allowedColumns []string) (string, error) {
	if orderBy == "" {
		return "", nil
	}

	parts := strings.Split(orderBy, " ")
	column := parts[0]

	allowed := false
	for _, col := range allowedColumns {
		if col == column {
			allowed = true
			break
		}
	}

	if !allowed {
		return "", fmt.Errorf("invalid order by column: %q", column)
	}

	direction := "ASC"
	if len(parts) > 1 && strings.ToUpper(parts[1]) == "DESC" {
		direction = "DESC"
	}

	return fmt.Sprintf(" ORDER BY %s %s", column, direction), nil
}

func BuildPaginationClause(limit, offset int) string {
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf(" LIMIT %d OFFSET %d", limit, offset)
}
