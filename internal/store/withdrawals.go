package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type WithdrawalStatus string

const (
	WithdrawalStatusBroadcasted WithdrawalStatus = "broadcasted"
	WithdrawalStatusFailed      WithdrawalStatus = "failed"
)

type Withdrawal struct {
	ID        string
	AccountID string
	WalletID  string
	ToAddress string
	AmountZat int64
	FeeZat    *int64
	TxID      *string
	Status    WithdrawalStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *Store) CreateWithdrawalAndDebit(ctx context.Context, accountID, walletID, toAddress string, amountZat, feeZat int64, txid string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return "", err
	}

	accountID = strings.TrimSpace(accountID)
	walletID = strings.TrimSpace(walletID)
	toAddress = strings.TrimSpace(toAddress)
	txid = strings.ToLower(strings.TrimSpace(txid))

	if accountID == "" || walletID == "" || toAddress == "" || txid == "" {
		return "", errors.New("store: invalid withdrawal params")
	}
	if amountZat <= 0 || feeZat < 0 {
		return "", errors.New("store: invalid amount/fee")
	}

	id, err := randomHex(16)
	if err != nil {
		return "", err
	}

	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	// Ensure account exists and debit.
	var bal int64
	if err := tx.QueryRowContext(ctx, `SELECT balance_zat FROM account_balances WHERE account_id=?`, accountID).Scan(&bal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("store: account not found")
		}
		return "", err
	}
	total := amountZat + feeZat
	if total < amountZat {
		return "", errors.New("store: amount overflow")
	}
	if bal < total {
		return "", errors.New("store: insufficient balance")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE account_balances SET balance_zat=?, updated_at=? WHERE account_id=?`, bal-total, now, accountID); err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO withdrawals(id, account_id, wallet_id, to_address, amount_zat, fee_zat, txid, status, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
	`, id, accountID, walletID, toAddress, amountZat, feeZat, txid, string(WithdrawalStatusBroadcasted), now, now); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) ListWithdrawals(ctx context.Context, accountID string, limit int) ([]Withdrawal, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return nil, err
	}
	accountID = strings.TrimSpace(accountID)
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	var rows *sql.Rows
	var err error
	if accountID == "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, account_id, wallet_id, to_address, amount_zat, fee_zat, txid, status, created_at, updated_at
			FROM withdrawals
			ORDER BY created_at DESC
			LIMIT ?
		`, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, account_id, wallet_id, to_address, amount_zat, fee_zat, txid, status, created_at, updated_at
			FROM withdrawals
			WHERE account_id=?
			ORDER BY created_at DESC
			LIMIT ?
		`, accountID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Withdrawal, 0, limit)
	for rows.Next() {
		var w Withdrawal
		var fee sql.NullInt64
		var txid sql.NullString
		var status string
		var created int64
		var updated int64
		if err := rows.Scan(&w.ID, &w.AccountID, &w.WalletID, &w.ToAddress, &w.AmountZat, &fee, &txid, &status, &created, &updated); err != nil {
			return nil, err
		}
		if fee.Valid {
			v := fee.Int64
			w.FeeZat = &v
		}
		if txid.Valid {
			v := strings.ToLower(strings.TrimSpace(txid.String))
			w.TxID = &v
		}
		w.Status = WithdrawalStatus(status)
		w.CreatedAt = time.Unix(created, 0)
		w.UpdatedAt = time.Unix(updated, 0)
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
