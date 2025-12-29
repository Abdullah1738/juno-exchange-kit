//go:build !cgo

package ffi

import "github.com/Abdullah1738/juno-exchange-kit/internal/keys"

type Deriver struct{}

func New() *Deriver { return &Deriver{} }

func (d *Deriver) UFVKFromSeedBase64(seedBase64 string, uaHRP string, coinType uint32, account uint32) (string, error) {
	return "", keys.ErrUnavailable
}

func (d *Deriver) AddressFromUFVK(ufvk string, uaHRP string, scope keys.Scope, index uint32) (string, error) {
	return "", keys.ErrUnavailable
}
