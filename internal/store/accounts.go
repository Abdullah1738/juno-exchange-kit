package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type AccountSummary struct {
	AccountID  string
	BalanceZat int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (s *Store) ListAccounts(ctx context.Context, limit int, offset int, minBalanceZat *int64) ([]AccountSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	if offset < 0 {
		return nil, errors.New("store: offset must be >= 0")
	}
	if minBalanceZat != nil && *minBalanceZat < 0 {
		return nil, errors.New("store: min balance must be >= 0")
	}

	q := `
		SELECT a.account_id, b.balance_zat, a.created_at, b.updated_at
		FROM accounts a
		JOIN account_balances b ON b.account_id = a.account_id
	`
	var args []any
	if minBalanceZat != nil {
		q += ` WHERE b.balance_zat >= ?`
		args = append(args, *minBalanceZat)
	}
	q += ` ORDER BY a.created_at ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]AccountSummary, 0, min(limit, 1000))
	for rows.Next() {
		var row AccountSummary
		var created int64
		var updated int64
		if err := rows.Scan(&row.AccountID, &row.BalanceZat, &created, &updated); err != nil {
			return nil, err
		}
		row.AccountID = strings.TrimSpace(row.AccountID)
		if row.AccountID == "" {
			return nil, errors.New("store: invalid account_id")
		}
		if created < 0 || updated < 0 {
			return nil, errors.New("store: invalid timestamp")
		}
		row.CreatedAt = time.Unix(created, 0).UTC()
		row.UpdatedAt = time.Unix(updated, 0).UTC()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) SumAccountBalances(ctx context.Context) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return 0, err
	}

	var sum sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT SUM(balance_zat) FROM account_balances`).Scan(&sum); err != nil {
		return 0, err
	}
	if !sum.Valid {
		return 0, nil
	}
	if sum.Int64 < 0 {
		return 0, fmt.Errorf("store: invalid balances sum: %d", sum.Int64)
	}
	return sum.Int64, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
