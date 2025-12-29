package store

import (
	"context"
	"testing"
	"time"
)

func TestListAccountsAndSumAccountBalances(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	a1, err := s.CreateAccount(ctx)
	if err != nil {
		t.Fatalf("CreateAccount a1: %v", err)
	}
	a2, err := s.CreateAccount(ctx)
	if err != nil {
		t.Fatalf("CreateAccount a2: %v", err)
	}

	// Make ordering deterministic.
	if _, err := s.db.ExecContext(ctx, `UPDATE accounts SET created_at=? WHERE account_id=?`, 100, a1); err != nil {
		t.Fatalf("update created_at a1: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE accounts SET created_at=? WHERE account_id=?`, 200, a2); err != nil {
		t.Fatalf("update created_at a2: %v", err)
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE account_balances SET balance_zat=?, updated_at=? WHERE account_id=?`, 10, 201, a1); err != nil {
		t.Fatalf("update balance a1: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE account_balances SET balance_zat=?, updated_at=? WHERE account_id=?`, 0, 202, a2); err != nil {
		t.Fatalf("update balance a2: %v", err)
	}

	sum, err := s.SumAccountBalances(ctx)
	if err != nil {
		t.Fatalf("SumAccountBalances: %v", err)
	}
	if sum != 10 {
		t.Fatalf("sum=%d want %d", sum, 10)
	}

	rows, err := s.ListAccounts(ctx, 100, 0, nil)
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows=%d want %d", len(rows), 2)
	}
	if rows[0].AccountID != a1 || rows[0].BalanceZat != 10 {
		t.Fatalf("row0=%+v want account_id=%s balance=10", rows[0], a1)
	}
	if rows[1].AccountID != a2 || rows[1].BalanceZat != 0 {
		t.Fatalf("row1=%+v want account_id=%s balance=0", rows[1], a2)
	}

	min := int64(1)
	filtered, err := s.ListAccounts(ctx, 100, 0, &min)
	if err != nil {
		t.Fatalf("ListAccounts(min): %v", err)
	}
	if len(filtered) != 1 || filtered[0].AccountID != a1 {
		t.Fatalf("filtered=%+v want only %s", filtered, a1)
	}
}

