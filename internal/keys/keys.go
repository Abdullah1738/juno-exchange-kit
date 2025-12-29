package keys

import "errors"

var ErrUnavailable = errors.New("keys: unavailable")

type Scope string

const (
	ScopeExternal Scope = "external"
	ScopeInternal Scope = "internal"
)

type Deriver interface {
	UFVKFromSeedBase64(seedBase64 string, uaHRP string, coinType uint32, account uint32) (string, error)
	AddressFromUFVK(ufvk string, uaHRP string, scope Scope, index uint32) (string, error)
}
