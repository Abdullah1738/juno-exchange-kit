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

- `junocashd` running locally with RPC enabled (regtest recommended for demos/tests).

## Run `junocashd` (example: regtest)

Use a dedicated data dir so you don’t mix it with any existing node data:

- Start:
  - `junocashd -regtest -datadir="$PWD/.junocashd-regtest" -rpcuser=juno -rpcpassword=juno -rpcport=18232 -rpcbind=127.0.0.1 -rpcallowip=127.0.0.1 -daemon`
- Verify:
  - `junocash-cli -regtest -datadir="$PWD/.junocashd-regtest" -rpcuser=juno -rpcpassword=juno -rpcport=18232 getblockchaininfo`
- Stop:
  - `junocash-cli -regtest -datadir="$PWD/.junocashd-regtest" -rpcuser=juno -rpcpassword=juno -rpcport=18232 stop`

## Planned CLI commands

- `juno-exchange-kit account create`
- `juno-exchange-kit account deposit-address <account_id>`
- `juno-exchange-kit sync`
- `juno-exchange-kit deposits list`
- `juno-exchange-kit sweep consolidate`
- `juno-exchange-kit sweep to-cold`
- `juno-exchange-kit withdraw --to <j1...> --amount <amt>`
- `juno-exchange-kit cold sign <txplan.json>` (offline/HSM simulation)
- `juno-exchange-kit withdrawals list`

Status: work in progress.
