package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	dataDir string
}

func Open(dataDir string) (*Store, error) {
	dataDir = filepath.Clean(dataDir)
	if dataDir == "." || dataDir == string(os.PathSeparator) {
		return nil, errors.New("store: invalid data dir")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("store: mkdir data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "state.sqlite")
	dsn := "file:" + dbPath + "?_pragma=busy_timeout(5000)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db, dataDir: dataDir}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Init(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store: nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS accounts (
			account_id TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS wallet_state (
			wallet_id TEXT PRIMARY KEY,
			next_external_index INTEGER NOT NULL,
			next_internal_index INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS deposit_addresses (
			account_id TEXT NOT NULL,
			wallet_id TEXT NOT NULL,
			scope TEXT NOT NULL,
			address_index INTEGER NOT NULL,
			address TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			PRIMARY KEY (account_id, wallet_id, scope),
			UNIQUE (wallet_id, scope, address_index)
		)`,
		`CREATE TABLE IF NOT EXISTS scan_cursors (
			wallet_id TEXT PRIMARY KEY,
			cursor INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS deposits (
			wallet_id TEXT NOT NULL,
			txid TEXT NOT NULL,
			action_index INTEGER NOT NULL,
			diversifier_index INTEGER NOT NULL,
			account_id TEXT,
			amount_zat INTEGER NOT NULL,
			height INTEGER NOT NULL,
			status TEXT NOT NULL,
			confirmed_height INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (wallet_id, txid, action_index)
		)`,
		`CREATE TABLE IF NOT EXISTS account_balances (
			account_id TEXT PRIMARY KEY,
			balance_zat INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS withdrawals (
			id TEXT PRIMARY KEY,
			account_id TEXT NOT NULL,
			wallet_id TEXT NOT NULL,
			to_address TEXT NOT NULL,
			amount_zat INTEGER NOT NULL,
			fee_zat INTEGER,
			txid TEXT,
			status TEXT NOT NULL,
			error_code TEXT,
			error_message TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

func (s *Store) CreateAccount(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return "", err
	}

	accountID, err := randomHex(16)
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `INSERT INTO accounts(account_id, created_at) VALUES(?,?)`, accountID, now); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_balances(account_id, balance_zat, updated_at) VALUES(?,?,?)`, accountID, 0, now); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return accountID, nil
}

// GetOrAssignDepositAddress allocates a deterministic external deposit address
// for the given account under the "hot" wallet, if it doesn't already exist.
//
// Note: address derivation is wired in later steps; for now this stores a placeholder.
func (s *Store) GetOrAssignDepositAddress(ctx context.Context, accountID string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return "", err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", errors.New("store: account_id required")
	}

	const walletID = "hot"
	const scope = "external"

	var existing string
	err := s.db.QueryRowContext(ctx, `SELECT address FROM deposit_addresses WHERE account_id=? AND wallet_id=? AND scope=?`, accountID, walletID, scope).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}

	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	// Ensure account exists.
	var tmp string
	if err := tx.QueryRowContext(ctx, `SELECT account_id FROM accounts WHERE account_id=?`, accountID).Scan(&tmp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("store: account not found")
		}
		return "", err
	}

	var nextIdx int64
	err = tx.QueryRowContext(ctx, `SELECT next_external_index FROM wallet_state WHERE wallet_id=?`, walletID).Scan(&nextIdx)
	if errors.Is(err, sql.ErrNoRows) {
		// Initialize wallet state lazily.
		if _, err := tx.ExecContext(ctx, `INSERT INTO wallet_state(wallet_id, next_external_index, next_internal_index, created_at) VALUES(?,?,?,?)`, walletID, 0, 0, now); err != nil {
			return "", err
		}
		nextIdx = 0
	} else if err != nil {
		return "", err
	}

	addr := fmt.Sprintf("ADDR_NOT_IMPLEMENTED_%d", nextIdx)
	if _, err := tx.ExecContext(ctx, `INSERT INTO deposit_addresses(account_id, wallet_id, scope, address_index, address, created_at) VALUES(?,?,?,?,?,?)`, accountID, walletID, scope, nextIdx, addr, now); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE wallet_state SET next_external_index=? WHERE wallet_id=?`, nextIdx+1, walletID); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return addr, nil
}

func randomHex(n int) (string, error) {
	if n <= 0 || n > 64 {
		return "", errors.New("store: invalid random len")
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
