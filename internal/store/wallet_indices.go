package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *Store) NextInternalAddress(ctx context.Context, walletID string, derive func(index uint32) (string, error)) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return "", err
	}
	walletID = strings.TrimSpace(walletID)
	if walletID == "" {
		return "", errors.New("store: wallet_id required")
	}
	if derive == nil {
		return "", errors.New("store: derive func is nil")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	var nextIdx int64
	if err := tx.QueryRowContext(ctx, `SELECT next_internal_index FROM wallets WHERE wallet_id=?`, walletID).Scan(&nextIdx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("store: wallet not initialized")
		}
		return "", err
	}
	if nextIdx < 0 || nextIdx > int64(^uint32(0)) {
		return "", errors.New("store: invalid wallet index")
	}

	addr, err := derive(uint32(nextIdx))
	if err != nil {
		return "", err
	}
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", errors.New("store: derived address empty")
	}

	if _, err := tx.ExecContext(ctx, `UPDATE wallets SET next_internal_index=? WHERE wallet_id=?`, nextIdx+1, walletID); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return addr, nil
}
