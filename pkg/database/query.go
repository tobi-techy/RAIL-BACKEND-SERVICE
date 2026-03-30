package database

import (
	"context"
	"fmt"
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

func BuildWhereClause(conditions map[string]interface{}) (string, []interface{}) {
	if len(conditions) == 0 {
		return "", nil
	}

	var clauses []string
	var args []interface{}
	paramIndex := 1

	for key, value := range conditions {
		clauses = append(clauses, fmt.Sprintf("%s = $%d", key, paramIndex))
		args = append(args, value)
		paramIndex++
	}

	return " WHERE " + strings.Join(clauses, " AND "), args
}

func BuildOrderByClause(orderBy string, allowedColumns []string) string {
	if orderBy == "" {
		return ""
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
		return ""
	}

	direction := "ASC"
	if len(parts) > 1 && strings.ToUpper(parts[1]) == "DESC" {
		direction = "DESC"
	}

	return fmt.Sprintf(" ORDER BY %s %s", column, direction)
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
