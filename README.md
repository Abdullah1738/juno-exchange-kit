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

Status: work in progress.
