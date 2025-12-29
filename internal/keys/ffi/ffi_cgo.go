//go:build cgo

package ffi

/*
#cgo CFLAGS: -I${SRCDIR}/../../../rust/keys/include
#cgo LDFLAGS: -L${SRCDIR}/../../../rust/keys/target/release -ljuno_keys

#include "juno_keys.h"
#include <stdlib.h>
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"github.com/Abdullah1738/juno-exchange-kit/internal/keys"
)

type Deriver struct{}

func New() *Deriver { return &Deriver{} }

func (d *Deriver) UFVKFromSeedBase64(seedBase64 string, uaHRP string, coinType uint32, account uint32) (string, error) {
	req := map[string]any{
		"seed_base64": seedBase64,
		"ua_hrp":      uaHRP,
		"coin_type":   coinType,
		"account":     account,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return "", errors.New("keys: marshal request")
	}

	cReq := C.CString(string(b))
	defer C.free(unsafe.Pointer(cReq))

	out := C.juno_keys_ufvk_from_seed_json(cReq)
	if out == nil {
		return "", keys.ErrUnavailable
	}
	defer C.juno_keys_string_free(out)

	var resp struct {
		Status string `json:"status"`
		UFVK   string `json:"ufvk,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(C.GoString(out)), &resp); err != nil {
		return "", errors.New("keys: invalid response")
	}
	switch resp.Status {
	case "ok":
		v := strings.TrimSpace(resp.UFVK)
		if v == "" {
			return "", errors.New("keys: invalid response")
		}
		return v, nil
	case "err":
		code := strings.TrimSpace(resp.Error)
		if code == "" {
			return "", errors.New("keys: failed")
		}
		return "", fmt.Errorf("keys: %s", code)
	default:
		return "", errors.New("keys: invalid response")
	}
}

func (d *Deriver) AddressFromUFVK(ufvk string, uaHRP string, scope keys.Scope, index uint32) (string, error) {
	req := map[string]any{
		"ufvk":    ufvk,
		"ua_hrp":  uaHRP,
		"scope":   scope,
		"index":   index,
		"version": 1,
	}
	b, err := json.Marshal(req)
	if err != nil {
		return "", errors.New("keys: marshal request")
	}

	cReq := C.CString(string(b))
	defer C.free(unsafe.Pointer(cReq))

	out := C.juno_keys_address_from_ufvk_json(cReq)
	if out == nil {
		return "", keys.ErrUnavailable
	}
	defer C.juno_keys_string_free(out)

	var resp struct {
		Status  string `json:"status"`
		Address string `json:"address,omitempty"`
		Error   string `json:"error,omitempty"`
	}
	if err := json.Unmarshal([]byte(C.GoString(out)), &resp); err != nil {
		return "", errors.New("keys: invalid response")
	}
	switch resp.Status {
	case "ok":
		v := strings.TrimSpace(resp.Address)
		if v == "" {
			return "", errors.New("keys: invalid response")
		}
		return v, nil
	case "err":
		code := strings.TrimSpace(resp.Error)
		if code == "" {
			return "", errors.New("keys: failed")
		}
		return "", fmt.Errorf("keys: %s", code)
	default:
		return "", errors.New("keys: invalid response")
	}
}
