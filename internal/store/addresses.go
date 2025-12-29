package store

import (
	"context"
	"errors"
	"strings"
	"time"
)

type DepositAddress struct {
	AccountID string
	WalletID  string
	Scope     string
	Index     uint32
	Address   string
	CreatedAt time.Time
}

func (s *Store) CountDepositAddresses(ctx context.Context, walletID string, accountID *string) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return 0, err
	}
	walletID = strings.TrimSpace(walletID)
	if walletID == "" {
		return 0, errors.New("store: wallet_id required")
	}

	q := `SELECT COUNT(1) FROM deposit_addresses WHERE wallet_id=? AND scope='external'`
	args := []any{walletID}
	if accountID != nil {
		v := strings.TrimSpace(*accountID)
		if v != "" {
			q += ` AND account_id=?`
			args = append(args, v)
		}
	}

	var n int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&n); err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, errors.New("store: invalid count")
	}
	return n, nil
}

func (s *Store) ListDepositAddresses(ctx context.Context, walletID string, accountID *string, limit int, offset int) ([]DepositAddress, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return nil, err
	}
	walletID = strings.TrimSpace(walletID)
	if walletID == "" {
		return nil, errors.New("store: wallet_id required")
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

	q := `
		SELECT account_id, wallet_id, scope, address_index, address, created_at
		FROM deposit_addresses
		WHERE wallet_id=? AND scope='external'
	`
	args := []any{walletID}
	if accountID != nil {
		v := strings.TrimSpace(*accountID)
		if v != "" {
			q += ` AND account_id=?`
			args = append(args, v)
		}
	}
	q += ` ORDER BY address_index ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]DepositAddress, 0, min(limit, 1000))
	for rows.Next() {
		var row DepositAddress
		var idx int64
		var created int64
		if err := rows.Scan(&row.AccountID, &row.WalletID, &row.Scope, &idx, &row.Address, &created); err != nil {
			return nil, err
		}
		row.AccountID = strings.TrimSpace(row.AccountID)
		row.WalletID = strings.TrimSpace(row.WalletID)
		row.Scope = strings.TrimSpace(row.Scope)
		row.Address = strings.TrimSpace(row.Address)
		if row.AccountID == "" || row.WalletID == "" || row.Scope == "" || row.Address == "" {
			return nil, errors.New("store: invalid deposit address row")
		}
		if idx < 0 || idx > int64(^uint32(0)) {
			return nil, errors.New("store: invalid address index")
		}
		row.Index = uint32(idx)
		if created < 0 {
			return nil, errors.New("store: invalid created_at")
		}
		row.CreatedAt = time.Unix(created, 0).UTC()
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
