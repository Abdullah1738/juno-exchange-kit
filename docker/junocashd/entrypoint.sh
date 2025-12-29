#!/bin/sh
set -eu

chain="${JUNO_CHAIN:-regtest}"

case "$chain" in
  regtest)
    exec junocashd -regtest -listen=0 "$@"
    ;;
  testnet)
    exec junocashd -testnet -listen=1 "$@"
    ;;
  mainnet|main)
    exec junocashd -listen=1 "$@"
    ;;
  *)
    echo "unknown JUNO_CHAIN: $chain (expected regtest|testnet|mainnet)" >&2
    exit 2
    ;;
esac

