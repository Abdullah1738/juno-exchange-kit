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

## Start all dependencies (Docker, regtest)

Bring up `junocashd` + `juno-scan` + `juno-broadcast`:

- `make up`

Tear down (also removes volumes):

- `make down`

Defaults:

- `junocashd` RPC: `http://127.0.0.1:18232` (user/pass: `rpcuser` / `rpcpass`)
- `juno-scan` API: `http://127.0.0.1:18080`
- `juno-broadcast` API: `http://127.0.0.1:18081`

Optional overrides:

- `JUNO_SCAN_CONFIRMATIONS=1` (useful on regtest to get `DepositConfirmed` quickly)
- `JUNO_SCAN_REF` / `JUNO_BROADCAST_REF` to change the pinned git ref used for Docker builds

## Build and run

Build:

- `make build` (outputs `bin/juno-exchange-kit`)

Set env vars to point at the Docker stack:

```sh
export JUNO_RPC_URL="http://127.0.0.1:18232"
export JUNO_RPC_USER="rpcuser"
export JUNO_RPC_PASS="rpcpass"

export JUNO_SCAN_URL="http://127.0.0.1:18080"
export JUNO_BROADCAST_URL="http://127.0.0.1:18081"

export JUNO_TXSIGN_BIN="/path/to/juno-txsign"
```

Commands:

- Init wallets + state: `bin/juno-exchange-kit init`
- Create account: `bin/juno-exchange-kit account create`
- Get deposit address: `bin/juno-exchange-kit account deposit-address <account_id>`
- Sync/credit confirmed deposits: `bin/juno-exchange-kit sync`
- Sweep hot balance to cold: `bin/juno-exchange-kit sweep to-cold`
- Consolidate hot notes: `bin/juno-exchange-kit sweep consolidate`
- Withdraw: `bin/juno-exchange-kit withdraw --account <id> --to <j...> --amount-zat <n>`
- Cold → hot (offline flow): `bin/juno-exchange-kit cold-to-hot plan|sign|broadcast`
- History: `bin/juno-exchange-kit withdrawals list`
