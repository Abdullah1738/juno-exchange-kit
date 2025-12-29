package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type DepositStatus string

const (
	DepositStatusDetected    DepositStatus = "detected"
	DepositStatusConfirmed   DepositStatus = "confirmed"
	DepositStatusUnconfirmed DepositStatus = "unconfirmed"
	DepositStatusOrphaned    DepositStatus = "orphaned"
)

type DepositApplyResult struct {
	AccountID string
	DeltaZat  int64
}

func (s *Store) AccountForDiversifierIndex(ctx context.Context, walletID string, diversifierIndex uint32) (string, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return "", false, err
	}
	walletID = strings.TrimSpace(walletID)
	if walletID == "" {
		return "", false, errors.New("store: wallet_id required")
	}

	var accountID string
	err := s.db.QueryRowContext(ctx, `
		SELECT account_id FROM deposit_addresses
		WHERE wallet_id=? AND scope='external' AND address_index=?
	`, walletID, diversifierIndex).Scan(&accountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", false, errors.New("store: invalid account_id mapping")
	}
	return accountID, true, nil
}

func (s *Store) GetScanCursor(ctx context.Context, walletID string) (int64, error) {
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
	var cur int64
	err := s.db.QueryRowContext(ctx, `SELECT cursor FROM scan_cursors WHERE wallet_id=?`, walletID).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		if err := s.EnsureScanCursor(ctx, walletID); err != nil {
			return 0, err
		}
		return 0, nil
	}
	return cur, err
}

func (s *Store) SetScanCursor(ctx context.Context, walletID string, cursor int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return err
	}
	walletID = strings.TrimSpace(walletID)
	if walletID == "" {
		return errors.New("store: wallet_id required")
	}
	if cursor < 0 {
		return errors.New("store: cursor must be >= 0")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO scan_cursors(wallet_id,cursor) VALUES(?,?) ON CONFLICT(wallet_id) DO UPDATE SET cursor=excluded.cursor`, walletID, cursor)
	return err
}

func (s *Store) ApplyDeposit(ctx context.Context, walletID, txid string, actionIndex uint32, diversifierIndex uint32, amountZat int64, height int64, status DepositStatus, confirmedHeight *int64) (DepositApplyResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return DepositApplyResult{}, err
	}

	walletID = strings.TrimSpace(walletID)
	txid = strings.ToLower(strings.TrimSpace(txid))
	if walletID == "" || txid == "" {
		return DepositApplyResult{}, errors.New("store: wallet_id and txid required")
	}
	if amountZat <= 0 {
		return DepositApplyResult{}, errors.New("store: amount_zat must be > 0")
	}
	if height < 0 {
		return DepositApplyResult{}, errors.New("store: height must be >= 0")
	}
	switch status {
	case DepositStatusDetected, DepositStatusConfirmed, DepositStatusUnconfirmed, DepositStatusOrphaned:
	default:
		return DepositApplyResult{}, errors.New("store: invalid deposit status")
	}

	accountID, ok, err := s.AccountForDiversifierIndex(ctx, walletID, diversifierIndex)
	if err != nil {
		return DepositApplyResult{}, err
	}
	if !ok {
		accountID = ""
	}

	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DepositApplyResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var prevStatus string
	var prevAccount sql.NullString
	err = tx.QueryRowContext(ctx, `
		SELECT status, account_id
		FROM deposits
		WHERE wallet_id=? AND txid=? AND action_index=?
	`, walletID, txid, actionIndex).Scan(&prevStatus, &prevAccount)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DepositApplyResult{}, err
	}

	// Prefer the previously stored account_id if present.
	if prevAccount.Valid && strings.TrimSpace(prevAccount.String) != "" {
		accountID = strings.TrimSpace(prevAccount.String)
	}

	if !found {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO deposits(wallet_id, txid, action_index, diversifier_index, account_id, amount_zat, height, status, confirmed_height, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)
		`, walletID, txid, actionIndex, diversifierIndex, nullIfEmpty(accountID), amountZat, height, string(status), confirmedHeight, now, now)
		if err != nil {
			return DepositApplyResult{}, err
		}
	} else {
		_, err := tx.ExecContext(ctx, `
			UPDATE deposits SET
				diversifier_index=?,
				account_id=?,
				amount_zat=?,
				height=?,
				status=?,
				confirmed_height=?,
				updated_at=?
			WHERE wallet_id=? AND txid=? AND action_index=?
		`, diversifierIndex, nullIfEmpty(accountID), amountZat, height, string(status), confirmedHeight, now, walletID, txid, actionIndex)
		if err != nil {
			return DepositApplyResult{}, err
		}
	}

	var delta int64
	switch {
	case prevStatus != string(DepositStatusConfirmed) && status == DepositStatusConfirmed:
		if accountID != "" {
			delta = amountZat
		}
	case prevStatus == string(DepositStatusConfirmed) && status != DepositStatusConfirmed:
		if accountID != "" {
			delta = -amountZat
		}
	}

	if delta != 0 {
		if err := applyAccountBalanceDeltaTx(ctx, tx, accountID, delta, now); err != nil {
			return DepositApplyResult{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return DepositApplyResult{}, err
	}
	return DepositApplyResult{AccountID: accountID, DeltaZat: delta}, nil
}

func applyAccountBalanceDeltaTx(ctx context.Context, tx *sql.Tx, accountID string, delta int64, nowUnix int64) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return errors.New("store: account_id required")
	}

	var bal int64
	err := tx.QueryRowContext(ctx, `SELECT balance_zat FROM account_balances WHERE account_id=?`, accountID).Scan(&bal)
	if err != nil {
		return err
	}
	next := bal + delta
	if next < 0 {
		return errors.New("store: negative balance")
	}
	_, err = tx.ExecContext(ctx, `UPDATE account_balances SET balance_zat=?, updated_at=? WHERE account_id=?`, next, nowUnix, accountID)
	return err
}

func nullIfEmpty(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}

