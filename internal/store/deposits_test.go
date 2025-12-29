package store

import (
	"context"
	"database/sql"
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
	res, err := s.ApplyDeposit(ctx, "hot", "txid", 0, 0, "jregtest1addr0", 100, 9, DepositStatusConfirmed, &ch)
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
	res, err = s.ApplyDeposit(ctx, "hot", "txid", 0, 0, "jregtest1addr0", 100, 9, DepositStatusConfirmed, &ch)
	if err != nil {
		t.Fatalf("ApplyDeposit confirmed (again): %v", err)
	}
	if res.DeltaZat != 0 {
		t.Fatalf("delta=%d want %d", res.DeltaZat, 0)
	}

	// Reorg/unconfirm should debit once.
	res, err = s.ApplyDeposit(ctx, "hot", "txid", 0, 0, "jregtest1addr0", 100, 9, DepositStatusUnconfirmed, nil)
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

func TestApplyDeposit_RecipientAddressBeatsDiversifierIndex(t *testing.T) {
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

	a1, err := s.CreateAccount(ctx)
	if err != nil {
		t.Fatalf("CreateAccount a1: %v", err)
	}
	a2, err := s.CreateAccount(ctx)
	if err != nil {
		t.Fatalf("CreateAccount a2: %v", err)
	}

	_, err = s.GetOrAssignDepositAddress(ctx, a1, "hot", func(index uint32) (string, error) {
		if index != 0 {
			t.Fatalf("unexpected index for a1: %d", index)
		}
		return "jregtest1addr0", nil
	})
	if err != nil {
		t.Fatalf("GetOrAssignDepositAddress a1: %v", err)
	}
	_, err = s.GetOrAssignDepositAddress(ctx, a2, "hot", func(index uint32) (string, error) {
		if index != 1 {
			t.Fatalf("unexpected index for a2: %d", index)
		}
		return "jregtest1addr1", nil
	})
	if err != nil {
		t.Fatalf("GetOrAssignDepositAddress a2: %v", err)
	}

	ch := int64(10)

	// A note to an internal/change address might share the same diversifier_index as a user's deposit
	// address. We must not credit based on diversifier_index alone.
	res, err := s.ApplyDeposit(ctx, "hot", "txid-internal", 0, 1, "jregtest1internal1", 123, 9, DepositStatusConfirmed, &ch)
	if err != nil {
		t.Fatalf("ApplyDeposit internal: %v", err)
	}
	if res.DeltaZat != 0 || res.AccountID != "" {
		t.Fatalf("unexpected internal credit: account=%q delta=%d", res.AccountID, res.DeltaZat)
	}

	bal2, err := s.AccountBalance(ctx, a2)
	if err != nil {
		t.Fatalf("AccountBalance a2: %v", err)
	}
	if bal2 != 0 {
		t.Fatalf("a2 bal=%d want %d", bal2, 0)
	}

	// A note to the actual external deposit address must credit.
	res, err = s.ApplyDeposit(ctx, "hot", "txid-deposit", 0, 1, "jregtest1addr1", 456, 9, DepositStatusConfirmed, &ch)
	if err != nil {
		t.Fatalf("ApplyDeposit deposit: %v", err)
	}
	if res.DeltaZat != 456 || res.AccountID != a2 {
		t.Fatalf("unexpected deposit credit: account=%q delta=%d", res.AccountID, res.DeltaZat)
	}
}

func TestApplyDeposit_DetectedMisattributedThenConfirmedDoesNotCredit(t *testing.T) {
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

	a, err := s.CreateAccount(ctx)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	_, err = s.GetOrAssignDepositAddress(ctx, a, "hot", func(index uint32) (string, error) {
		if index != 0 {
			t.Fatalf("unexpected index: %d", index)
		}
		return "jregtest1addr0", nil
	})
	if err != nil {
		t.Fatalf("GetOrAssignDepositAddress: %v", err)
	}

	// Simulate old behavior: detected event had no recipient_address, so it fell back to diversifier_index.
	_, err = s.ApplyDeposit(ctx, "hot", "txid", 0, 0, "", 100, 9, DepositStatusDetected, nil)
	if err != nil {
		t.Fatalf("ApplyDeposit detected: %v", err)
	}

	// Now the confirmed event includes recipient_address, but it doesn't match any external deposit address
	// (e.g. it's a change/internal address). This must not credit the account.
	ch := int64(10)
	res, err := s.ApplyDeposit(ctx, "hot", "txid", 0, 0, "jregtest1internal0", 100, 9, DepositStatusConfirmed, &ch)
	if err != nil {
		t.Fatalf("ApplyDeposit confirmed: %v", err)
	}
	if res.DeltaZat != 0 || res.AccountID != "" {
		t.Fatalf("unexpected credit: account=%q delta=%d", res.AccountID, res.DeltaZat)
	}

	bal, err := s.AccountBalance(ctx, a)
	if err != nil {
		t.Fatalf("AccountBalance: %v", err)
	}
	if bal != 0 {
		t.Fatalf("bal=%d want %d", bal, 0)
	}

	var acct sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT account_id FROM deposits WHERE wallet_id=? AND txid=? AND action_index=?`, "hot", "txid", 0).Scan(&acct); err != nil {
		t.Fatalf("select deposit: %v", err)
	}
	if acct.Valid && acct.String != "" {
		t.Fatalf("expected cleared account_id, got %q", acct.String)
	}
}
