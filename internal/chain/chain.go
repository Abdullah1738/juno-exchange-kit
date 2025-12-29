package chain

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Abdullah1738/juno-sdk-go/junocashd"
)

type Info struct {
	Chain    string
	Height   int64
	BranchID uint32
}

func GetInfo(ctx context.Context, rpc *junocashd.Client) (Info, error) {
	if rpc == nil {
		return Info{}, errors.New("chain: rpc is nil")
	}

	var resp struct {
		Chain     string `json:"chain"`
		Blocks    int64  `json:"blocks"`
		Consensus struct {
			Chaintip string `json:"chaintip"`
		} `json:"consensus"`
	}
	if err := rpc.Call(ctx, "getblockchaininfo", nil, &resp); err != nil {
		return Info{}, err
	}

	chain := strings.TrimSpace(resp.Chain)
	if chain == "" {
		return Info{}, errors.New("chain: missing chain")
	}
	chaintip := strings.TrimSpace(resp.Consensus.Chaintip)
	if chaintip == "" {
		return Info{}, errors.New("chain: missing consensus.chaintip")
	}
	branchU64, err := strconv.ParseUint(chaintip, 16, 32)
	if err != nil {
		return Info{}, errors.New("chain: invalid consensus.chaintip")
	}
	return Info{
		Chain:    chain,
		Height:   resp.Blocks,
		BranchID: uint32(branchU64),
	}, nil
}
