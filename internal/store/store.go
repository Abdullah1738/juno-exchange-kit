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
		`CREATE TABLE IF NOT EXISTS wallets (
			wallet_id TEXT PRIMARY KEY,
			ufvk TEXT NOT NULL,
			seed_path TEXT NOT NULL,
			next_external_index INTEGER NOT NULL,
			next_internal_index INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			disabled_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS accounts (
			account_id TEXT PRIMARY KEY,
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

func (s *Store) UpsertWallet(ctx context.Context, walletID string, ufvk string, seedPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return err
	}
	walletID = strings.TrimSpace(walletID)
	ufvk = strings.TrimSpace(ufvk)
	seedPath = strings.TrimSpace(seedPath)
	if walletID == "" || ufvk == "" || seedPath == "" {
		return errors.New("store: wallet_id, ufvk, seed_path required")
	}

	now := time.Now().Unix()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wallets(wallet_id, ufvk, seed_path, next_external_index, next_internal_index, created_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(wallet_id) DO UPDATE SET
			ufvk=excluded.ufvk,
			seed_path=excluded.seed_path
	`, walletID, ufvk, seedPath, 0, 0, now)
	return err
}

type Wallet struct {
	WalletID           string
	UFVK               string
	SeedPath           string
	NextExternalIndex  uint32
	NextInternalIndex  uint32
	CreatedAt          time.Time
	DisabledAtUnixSecs *int64
}

func (s *Store) Wallet(ctx context.Context, walletID string) (Wallet, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return Wallet{}, false, err
	}
	walletID = strings.TrimSpace(walletID)
	if walletID == "" {
		return Wallet{}, false, errors.New("store: wallet_id required")
	}

	var w Wallet
	var nextExt int64
	var nextInt int64
	var created int64
	var disabled sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT wallet_id, ufvk, seed_path, next_external_index, next_internal_index, created_at, disabled_at
		FROM wallets
		WHERE wallet_id=?
	`, walletID).Scan(&w.WalletID, &w.UFVK, &w.SeedPath, &nextExt, &nextInt, &created, &disabled)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Wallet{}, false, nil
		}
		return Wallet{}, false, err
	}
	if nextExt < 0 || nextExt > int64(^uint32(0)) || nextInt < 0 || nextInt > int64(^uint32(0)) {
		return Wallet{}, false, errors.New("store: invalid wallet index")
	}
	w.NextExternalIndex = uint32(nextExt)
	w.NextInternalIndex = uint32(nextInt)
	w.CreatedAt = time.Unix(created, 0)
	if disabled.Valid {
		v := disabled.Int64
		w.DisabledAtUnixSecs = &v
	}
	return w, true, nil
}

func (s *Store) EnsureScanCursor(ctx context.Context, walletID string) error {
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
	_, err := s.db.ExecContext(ctx, `INSERT INTO scan_cursors(wallet_id, cursor) VALUES(?,0) ON CONFLICT(wallet_id) DO NOTHING`, walletID)
	return err
}

func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("store: meta key required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) Meta(ctx context.Context, key string) (string, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.Init(ctx); err != nil {
		return "", false, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false, errors.New("store: meta key required")
	}
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, key).Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return v, true, nil
}

// GetOrAssignDepositAddress allocates a deterministic external deposit address
// for the given account under the given wallet, if it doesn't already exist.
func (s *Store) GetOrAssignDepositAddress(ctx context.Context, accountID string, walletID string, derive func(index uint32) (string, error)) (string, error) {
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
	if derive == nil {
		return "", errors.New("store: derive func is nil")
	}
	walletID = strings.TrimSpace(walletID)
	if walletID == "" {
		return "", errors.New("store: wallet_id required")
	}
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
	if err := tx.QueryRowContext(ctx, `SELECT next_external_index FROM wallets WHERE wallet_id=?`, walletID).Scan(&nextIdx); err != nil {
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

	if _, err := tx.ExecContext(ctx, `INSERT INTO deposit_addresses(account_id, wallet_id, scope, address_index, address, created_at) VALUES(?,?,?,?,?,?)`, accountID, walletID, scope, nextIdx, addr, now); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE wallets SET next_external_index=? WHERE wallet_id=?`, nextIdx+1, walletID); err != nil {
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
