package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestListDepositAddressesAndCount(t *testing.T) {
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
		return "addr-" + itoa(index), nil
	})
	if err != nil {
		t.Fatalf("GetOrAssignDepositAddress a1: %v", err)
	}
	_, err = s.GetOrAssignDepositAddress(ctx, a2, "hot", func(index uint32) (string, error) {
		return "addr-" + itoa(index), nil
	})
	if err != nil {
		t.Fatalf("GetOrAssignDepositAddress a2: %v", err)
	}

	n, err := s.CountDepositAddresses(ctx, "hot", nil)
	if err != nil {
		t.Fatalf("CountDepositAddresses: %v", err)
	}
	if n != 2 {
		t.Fatalf("count=%d want %d", n, 2)
	}

	accountFilter := a1
	n, err = s.CountDepositAddresses(ctx, "hot", &accountFilter)
	if err != nil {
		t.Fatalf("CountDepositAddresses(account): %v", err)
	}
	if n != 1 {
		t.Fatalf("count=%d want %d", n, 1)
	}

	addrs, err := s.ListDepositAddresses(ctx, "hot", nil, 100, 0)
	if err != nil {
		t.Fatalf("ListDepositAddresses: %v", err)
	}
	if len(addrs) != 2 {
		t.Fatalf("len(addrs)=%d want %d", len(addrs), 2)
	}
	if addrs[0].Index != 0 || addrs[1].Index != 1 {
		t.Fatalf("unexpected indices: %+v", addrs)
	}
}

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	out := make([]byte, 0, 10)
	for v > 0 {
		d := v % 10
		out = append([]byte{byte('0' + d)}, out...)
		v /= 10
	}
	return string(out)
}

