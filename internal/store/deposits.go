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

type DepositsSummary struct {
	Count     int64
	AmountZat int64
}

func (s *Store) PendingDepositsSummary(ctx context.Context, accountID *string) (DepositsSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return DepositsSummary{}, err
	}

	q := `
		SELECT COUNT(1), COALESCE(SUM(amount_zat), 0)
		FROM deposits
		WHERE account_id IS NOT NULL
		  AND status IN ('detected','unconfirmed')
	`
	args := []any{}
	if accountID != nil {
		v := strings.TrimSpace(*accountID)
		if v == "" {
			return DepositsSummary{}, errors.New("store: account_id required")
		}
		q += ` AND account_id=?`
		args = append(args, v)
	}

	var count int64
	var sum int64
	if err := s.db.QueryRowContext(ctx, q, args...).Scan(&count, &sum); err != nil {
		return DepositsSummary{}, err
	}
	if count < 0 || sum < 0 {
		return DepositsSummary{}, errors.New("store: invalid pending deposits summary")
	}
	return DepositsSummary{Count: count, AmountZat: sum}, nil
}

type DepositRecord struct {
	WalletID         string
	TxID             string
	ActionIndex      uint32
	DiversifierIndex uint32
	AccountID        *string
	AmountZat        int64
	Height           int64
	Status           DepositStatus
	ConfirmedHeight  *int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (s *Store) ListAccountDepositsSince(ctx context.Context, accountID string, sinceUnix int64, limit int) ([]DepositRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return nil, err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, errors.New("store: account_id required")
	}
	if sinceUnix < 0 {
		sinceUnix = 0
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT wallet_id, txid, action_index, diversifier_index, account_id, amount_zat, height, status, confirmed_height, created_at, updated_at
		FROM deposits
		WHERE account_id=?
		  AND updated_at >= ?
		ORDER BY updated_at ASC, wallet_id ASC, txid ASC, action_index ASC
		LIMIT ?
	`, accountID, sinceUnix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]DepositRecord, 0, min(limit, 1000))
	for rows.Next() {
		var r DepositRecord
		var actionIdx int64
		var divIdx int64
		var acct sql.NullString
		var confirmed sql.NullInt64
		var created int64
		var updated int64
		var status string
		if err := rows.Scan(&r.WalletID, &r.TxID, &actionIdx, &divIdx, &acct, &r.AmountZat, &r.Height, &status, &confirmed, &created, &updated); err != nil {
			return nil, err
		}
		r.WalletID = strings.TrimSpace(r.WalletID)
		r.TxID = strings.ToLower(strings.TrimSpace(r.TxID))
		if r.WalletID == "" || r.TxID == "" {
			return nil, errors.New("store: invalid deposit row")
		}
		if actionIdx < 0 || actionIdx > int64(^uint32(0)) {
			return nil, errors.New("store: invalid action_index")
		}
		if divIdx < 0 || divIdx > int64(^uint32(0)) {
			return nil, errors.New("store: invalid diversifier_index")
		}
		r.ActionIndex = uint32(actionIdx)
		r.DiversifierIndex = uint32(divIdx)
		if acct.Valid && strings.TrimSpace(acct.String) != "" {
			v := strings.TrimSpace(acct.String)
			r.AccountID = &v
		}
		r.Status = DepositStatus(strings.TrimSpace(status))
		switch r.Status {
		case DepositStatusDetected, DepositStatusConfirmed, DepositStatusUnconfirmed, DepositStatusOrphaned:
		default:
			return nil, errors.New("store: invalid deposit status")
		}
		if confirmed.Valid {
			ch := confirmed.Int64
			r.ConfirmedHeight = &ch
		}
		if created < 0 || updated < 0 {
			return nil, errors.New("store: invalid timestamp")
		}
		r.CreatedAt = time.Unix(created, 0).UTC()
		r.UpdatedAt = time.Unix(updated, 0).UTC()

		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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

func (s *Store) AccountForRecipientAddress(ctx context.Context, walletID string, recipientAddress string) (string, bool, error) {
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
	recipientAddress = strings.ToLower(strings.TrimSpace(recipientAddress))
	if recipientAddress == "" {
		return "", false, errors.New("store: recipient_address required")
	}

	var accountID string
	err := s.db.QueryRowContext(ctx, `
		SELECT account_id FROM deposit_addresses
		WHERE wallet_id=? AND scope='external' AND address=?
	`, walletID, recipientAddress).Scan(&accountID)
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

func (s *Store) ApplyDeposit(ctx context.Context, walletID, txid string, actionIndex uint32, diversifierIndex uint32, recipientAddress string, amountZat int64, height int64, status DepositStatus, confirmedHeight *int64) (DepositApplyResult, error) {
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

	recipientAddress = strings.ToLower(strings.TrimSpace(recipientAddress))

	accountID := ""
	if recipientAddress != "" {
		if a, ok, err := s.AccountForRecipientAddress(ctx, walletID, recipientAddress); err != nil {
			return DepositApplyResult{}, err
		} else if ok {
			accountID = a
		}
	} else {
		if a, ok, err := s.AccountForDiversifierIndex(ctx, walletID, diversifierIndex); err != nil {
			return DepositApplyResult{}, err
		} else if ok {
			accountID = a
		}
	}

	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DepositApplyResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var prevStatus string
	var prevAccount sql.NullString
	var prevAmount int64
	err = tx.QueryRowContext(ctx, `
		SELECT status, account_id, amount_zat
		FROM deposits
		WHERE wallet_id=? AND txid=? AND action_index=?
	`, walletID, txid, actionIndex).Scan(&prevStatus, &prevAccount, &prevAmount)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DepositApplyResult{}, err
	}

	prevAccountID := ""
	if prevAccount.Valid {
		prevAccountID = strings.TrimSpace(prevAccount.String)
	}

	// For backwards compatibility: if we don't have recipient_address, keep the previously stored mapping.
	if recipientAddress == "" && prevAccountID != "" {
		accountID = prevAccountID
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

	var deltas []struct {
		accountID string
		deltaZat  int64
	}
	switch {
	case prevStatus != string(DepositStatusConfirmed) && status == DepositStatusConfirmed:
		if accountID != "" {
			deltas = append(deltas, struct {
				accountID string
				deltaZat  int64
			}{accountID: accountID, deltaZat: amountZat})
		}
	case prevStatus == string(DepositStatusConfirmed) && status != DepositStatusConfirmed:
		if prevAccountID != "" {
			deltas = append(deltas, struct {
				accountID string
				deltaZat  int64
			}{accountID: prevAccountID, deltaZat: -prevAmount})
		}
	case prevStatus == string(DepositStatusConfirmed) && status == DepositStatusConfirmed:
		if prevAccountID != accountID || prevAmount != amountZat {
			if prevAccountID != "" {
				deltas = append(deltas, struct {
					accountID string
					deltaZat  int64
				}{accountID: prevAccountID, deltaZat: -prevAmount})
			}
			if accountID != "" {
				deltas = append(deltas, struct {
					accountID string
					deltaZat  int64
				}{accountID: accountID, deltaZat: amountZat})
			}
		}
	}

	var deltaSum int64
	for _, d := range deltas {
		if d.deltaZat == 0 || strings.TrimSpace(d.accountID) == "" {
			continue
		}
		if err := applyAccountBalanceDeltaTx(ctx, tx, d.accountID, d.deltaZat, now); err != nil {
			return DepositApplyResult{}, err
		}
		deltaSum += d.deltaZat
	}

	if err := tx.Commit(); err != nil {
		return DepositApplyResult{}, err
	}
	return DepositApplyResult{AccountID: accountID, DeltaZat: deltaSum}, nil
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
