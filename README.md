# juno-exchange-kit

Local CLI “exchange harness” for Juno Cash exchange integrations.

It is intended to demonstrate and validate the full flow end-to-end:

- create internal accounts
- derive per-account deposit addresses (UFVK + index)
- detect/credit deposits (shielded scanning)
- sweep/consolidate balances and rebalance hot <-> cold
- withdrawals with clear operational errors (e.g., `NO_LIQUIDITY_IN_HOT`, `INSUFFICIENT_BALANCE`)
- cold -> hot top-ups using an offline signing flow (HSM/airgap simulation)
- withdrawal history

## Prerequisites

- Docker (recommended for running all required services locally)
- Go 1.24+ and Rust (builds this binary and its key-derivation library)
- `juno-txsign` (required for signing withdrawals/sweeps; set `JUNO_TXSIGN_BIN` to its path)

## Start all dependencies (Docker)

Bring up `junocashd` + `juno-scan` + `juno-broadcast`:

- `make up`

Networks:

- Regtest (default): `make up`, `make up regtest`, or `make up-regtest`
- Testnet: `make up testnet` or `make up-testnet`
- Mainnet: `make up mainnet` or `make up-mainnet` (will take significant time/disk to sync)

Tear down (also removes volumes):

- `make down`

Defaults:

- `junocashd` RPC: `http://127.0.0.1:${JUNO_RPC_PORT_HOST:-28232}` (user/pass: `rpcuser` / `rpcpass`)
- `juno-scan` API: `http://127.0.0.1:${JUNO_SCAN_PORT_HOST:-18080}`
- `juno-broadcast` API: `http://127.0.0.1:${JUNO_BROADCAST_PORT_HOST:-18081}`

Optional overrides:

- `JUNO_RPC_PORT_HOST`, `JUNO_SCAN_PORT_HOST`, `JUNO_BROADCAST_PORT_HOST` (change host ports if something is already bound)
- `JUNO_CHAIN=regtest|testnet|mainnet` (defaults to `regtest`)
- `JUNOCASH_VERSION`, `JUNO_SCAN_REF`, `JUNO_BROADCAST_REF` (pins Docker builds)
- `JUNO_SCAN_CONFIRMATIONS` (defaults: `1` on regtest, `100` otherwise)

Note: `make up` builds the dependency images with `docker build` and runs Compose with `--no-build` to avoid `buildx`/cloud builder limits.

## Regtest controls (Docker)

The `junocashd` container includes a `juno-cli` helper that is preconfigured for the running chain + RPC creds.

Examples:

- Mine blocks: `docker compose exec junocashd juno-cli generate 101`
- Create a new unified account: `docker compose exec junocashd juno-cli z_getnewaccount`
- Get a deposit address for account 0: `docker compose exec junocashd juno-cli z_getaddressforaccount 0`
- Shield coinbase to an address: `docker compose exec junocashd juno-cli z_shieldcoinbase "*" "<j...>"`
- Poll async ops: `docker compose exec junocashd juno-cli z_getoperationresult '["<opid>"]'`

## Build and run

Build:

- `make build` (outputs `bin/juno-exchange-kit`)

## Data dir per network (important)

The exchange kit stores keys + state in a local data dir. Do **not** reuse the same data dir across networks.

Recommended:

- Regtest: `export JUNO_EXCHANGE_KIT_DATA_DIR="$HOME/.juno-exchange-kit/regtest"`
- Testnet: `export JUNO_EXCHANGE_KIT_DATA_DIR="$HOME/.juno-exchange-kit/testnet"`
- Mainnet: `export JUNO_EXCHANGE_KIT_DATA_DIR="$HOME/.juno-exchange-kit/mainnet"`

If you see a `jregtest1...` address while connected to mainnet, you are using a regtest-initialized data dir.

Set env vars to point at the Docker stack:

```sh
export JUNO_RPC_URL="http://127.0.0.1:28232"
export JUNO_RPC_USER="rpcuser"
export JUNO_RPC_PASS="rpcpass"

export JUNO_SCAN_URL="http://127.0.0.1:18080"
export JUNO_BROADCAST_URL="http://127.0.0.1:18081"

export JUNO_TXSIGN_BIN="/path/to/juno-txsign"
```

If you override host ports (e.g. `JUNO_RPC_PORT_HOST`), update the URLs above to match.

Commands:

- Init wallets + state: `bin/juno-exchange-kit init`
- Create account: `bin/juno-exchange-kit account create`
- Get deposit address: `bin/juno-exchange-kit account deposit-address <account_id>`
- Balance (confirmed credits only): `bin/juno-exchange-kit account balance <account_id>`
- Balance (includes pending deposits): `bin/juno-exchange-kit account balance <account_id> --json`
- List accounts + balances: `bin/juno-exchange-kit account list`
- Wait for deposit updates + exit on credit: `bin/juno-exchange-kit account wait-deposit <account_id> [--lookback 1h]`
- One-shot sync (consumes scanner events): `bin/juno-exchange-kit sync`
- Background sync loop: `bin/juno-exchange-kit daemon start` (use `daemon status` / `daemon stop`)
- Foreground sync loop: `bin/juno-exchange-kit daemon`
- Exchange balances (assets vs liabilities): `bin/juno-exchange-kit balances`
- Wallet balance (hot/cold): `bin/juno-exchange-kit wallet balance <hot|cold>`
- Wallet addresses: `bin/juno-exchange-kit wallet addresses <hot|cold> --scope external|internal|all`
- Sweep hot balance to cold: `bin/juno-exchange-kit sweep to-cold`
- Consolidate hot notes: `bin/juno-exchange-kit sweep consolidate` (reduces note count by sending to an internal hot address)
- Withdraw: `bin/juno-exchange-kit withdraw --account <id> --to <j...> --amount-zat <n>`
- Cold → hot (offline flow): `bin/juno-exchange-kit cold-to-hot plan|sign|broadcast`
- History: `bin/juno-exchange-kit withdrawals list`

## Deposits: pending vs confirmed

Because Juno Cash is shielded-by-default, deposits are detected by trial-decrypt scanning (UFVK) and then confirmed after N blocks.

Flow:

1. `juno-scan` emits `DepositEvent` when a deposit is detected (pending).
2. After `JUNO_SCAN_CONFIRMATIONS` blocks, `juno-scan` emits `DepositConfirmed`.
3. `juno-exchange-kit sync` / `daemon` consumes those events:
   - pending deposits are recorded immediately
   - account credits (liabilities) only increase on `DepositConfirmed`

Visibility:

- `bin/juno-exchange-kit balances` shows:
  - `liabilities_zat` (confirmed user balances)
  - `liabilities_pending_zat` (detected but not yet credited)
  - `equity_zat` (assets - confirmed liabilities)
  - `equity_total_zat` (assets - (confirmed + pending))
- `bin/juno-exchange-kit account wait-deposit <account_id>` prints `NEW_PENDING_DEPOSIT` / `NEW_CONFIRMED_DEPOSIT` lines (with a lookback window) and exits once the account is credited.

Example:

```
NEW_PENDING_DEPOSIT 1.0 JUNO conf=3 12 seconds ago
NEW_CONFIRMED_DEPOSIT 1.0 JUNO conf=100 now
```

Tip: on mainnet/testnet the default is `JUNO_SCAN_CONFIRMATIONS=100`, so deposits will sit in `liabilities_pending_zat` until confirmed. `balances` syncs from `juno-scan` by default; disable with `--sync=false`. For “always-on” behavior, use `bin/juno-exchange-kit daemon start` (logs to `<data-dir>/daemon.log` by default).
