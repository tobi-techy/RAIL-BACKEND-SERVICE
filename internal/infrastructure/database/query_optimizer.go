package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

type QueryOptimizer struct {
	db     *sql.DB
	logger *zap.Logger
}

func NewQueryOptimizer(db *sql.DB, logger *zap.Logger) *QueryOptimizer {
	return &QueryOptimizer{
		db:     db,
		logger: logger,
	}
}

func (qo *QueryOptimizer) ExplainQuery(ctx context.Context, query string, args ...interface{}) error {
	explainQuery := "EXPLAIN ANALYZE " + query
	
	rows, err := qo.db.QueryContext(ctx, explainQuery, args...)
	if err != nil {
		return fmt.Errorf("failed to explain query: %w", err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		plan.WriteString(line + "\n")
	}

	qo.logger.Info("Query execution plan", zap.String("plan", plan.String()))
	return nil
}

func (qo *QueryOptimizer) LogSlowQuery(ctx context.Context, query string, duration time.Duration, threshold time.Duration) {
	if duration > threshold {
		qo.logger.Warn("Slow query detected",
			zap.String("query", query),
			zap.Duration("duration", duration),
			zap.Duration("threshold", threshold),
		)
	}
}

func (qo *QueryOptimizer) PrepareStatement(ctx context.Context, query string) (*sql.Stmt, error) {
	stmt, err := qo.db.PrepareContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare statement: %w", err)
	}
	return stmt, nil
}

type QueryBuilder struct {
	query  strings.Builder
	args   []interface{}
	argIdx int
}

func NewQueryBuilder() *QueryBuilder {
	return &QueryBuilder{
		args:   make([]interface{}, 0),
		argIdx: 1,
	}
}

func (qb *QueryBuilder) Append(sql string, args ...interface{}) *QueryBuilder {
	qb.query.WriteString(sql)
	qb.args = append(qb.args, args...)
	return qb
}

func (qb *QueryBuilder) Where(condition string, args ...interface{}) *QueryBuilder {
	if qb.argIdx == 1 {
		qb.query.WriteString(" WHERE ")
	} else {
		qb.query.WriteString(" AND ")
	}
	qb.query.WriteString(condition)
	qb.args = append(qb.args, args...)
	qb.argIdx += len(args)
	return qb
}

func (qb *QueryBuilder) OrderBy(column string, desc bool) *QueryBuilder {
	qb.query.WriteString(" ORDER BY " + column)
	if desc {
		qb.query.WriteString(" DESC")
	}
	return qb
}

func (qb *QueryBuilder) Limit(limit int) *QueryBuilder {
	qb.query.WriteString(fmt.Sprintf(" LIMIT %d", limit))
	return qb
}

func (qb *QueryBuilder) Offset(offset int) *QueryBuilder {
	qb.query.WriteString(fmt.Sprintf(" OFFSET %d", offset))
	return qb
}

func (qb *QueryBuilder) Build() (string, []interface{}) {
	return qb.query.String(), qb.args
}
