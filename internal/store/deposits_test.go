package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestApplyDeposit_ConfirmedThenUnconfirmed_AdjustsBalanceOnce(t *testing.T) {
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

	if err := s.UpsertWallet(ctx, "hot", "ufvk", filepath.Join(t.TempDir(), "hot.seed")); err != nil {
		t.Fatalf("UpsertWallet: %v", err)
	}

	accountID, err := s.CreateAccount(ctx)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	_, err = s.GetOrAssignDepositAddress(ctx, accountID, "hot", func(index uint32) (string, error) {
		if index != 0 {
			t.Fatalf("unexpected index: %d", index)
		}
		return "jregtest1addr0", nil
	})
	if err != nil {
		t.Fatalf("GetOrAssignDepositAddress: %v", err)
	}

	ch := int64(10)
	res, err := s.ApplyDeposit(ctx, "hot", "txid", 0, 0, 100, 9, DepositStatusConfirmed, &ch)
	if err != nil {
		t.Fatalf("ApplyDeposit confirmed: %v", err)
	}
	if res.DeltaZat != 100 {
		t.Fatalf("delta=%d want %d", res.DeltaZat, 100)
	}

	bal, err := s.AccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("AccountBalance: %v", err)
	}
	if bal != 100 {
		t.Fatalf("bal=%d want %d", bal, 100)
	}

	// Re-applying the same confirmed event must be idempotent.
	res, err = s.ApplyDeposit(ctx, "hot", "txid", 0, 0, 100, 9, DepositStatusConfirmed, &ch)
	if err != nil {
		t.Fatalf("ApplyDeposit confirmed (again): %v", err)
	}
	if res.DeltaZat != 0 {
		t.Fatalf("delta=%d want %d", res.DeltaZat, 0)
	}

	// Reorg/unconfirm should debit once.
	res, err = s.ApplyDeposit(ctx, "hot", "txid", 0, 0, 100, 9, DepositStatusUnconfirmed, nil)
	if err != nil {
		t.Fatalf("ApplyDeposit unconfirmed: %v", err)
	}
	if res.DeltaZat != -100 {
		t.Fatalf("delta=%d want %d", res.DeltaZat, -100)
	}

	bal, err = s.AccountBalance(ctx, accountID)
	if err != nil {
		t.Fatalf("AccountBalance: %v", err)
	}
	if bal != 0 {
		t.Fatalf("bal=%d want %d", bal, 0)
	}
}

