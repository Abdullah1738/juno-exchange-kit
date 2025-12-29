package txplan

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/Abdullah1738/juno-exchange-kit/internal/chain"
	"github.com/Abdullah1738/juno-sdk-go/types"
)

func splitNotesBalanced(notes []spendableNote, maxSpends int) [][]spendableNote {
	if maxSpends <= 0 {
		maxSpends = len(notes)
	}
	if len(notes) <= maxSpends {
		return [][]spendableNote{notes}
	}

	k := (len(notes) + maxSpends - 1) / maxSpends
	if k <= 1 {
		return [][]spendableNote{notes}
	}

	sorted := append([]spendableNote(nil), notes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ValueZat != sorted[j].ValueZat {
			return sorted[i].ValueZat > sorted[j].ValueZat
		}
		if sorted[i].TxID != sorted[j].TxID {
			return sorted[i].TxID < sorted[j].TxID
		}
		return sorted[i].ActionIndex < sorted[j].ActionIndex
	})

	chunks := make([][]spendableNote, k)
	for i, n := range sorted {
		chunks[i%k] = append(chunks[i%k], n)
	}

	// Avoid 1-input transactions when we can, because the fee floor is 2 actions.
	// Only borrow from a chunk if it can still keep 2 notes.
	for {
		oneIdx := -1
		for i := range chunks {
			if len(chunks[i]) == 1 {
				oneIdx = i
				break
			}
		}
		if oneIdx == -1 {
			break
		}

		donor := -1
		maxLen := 0
		for i := range chunks {
			if len(chunks[i]) > maxLen {
				maxLen = len(chunks[i])
				donor = i
			}
		}
		if donor == -1 || donor == oneIdx || len(chunks[donor]) <= 2 {
			break
		}

		moved := chunks[donor][len(chunks[donor])-1]
		chunks[donor] = chunks[donor][:len(chunks[donor])-1]
		chunks[oneIdx] = append(chunks[oneIdx], moved)
	}

	return chunks
}

func planSweepWithNotes(
	ctx context.Context,
	cfg SendConfig,
	chainInfo chain.Info,
	coinType uint32,
	expiryHeight uint32,
	notes []spendableNote,
	toAddress string,
	memoHex string,
	changeAddress string,
	blockCache map[int64]blockV2,
) (types.TxPlan, uint64, error) {
	if len(notes) == 0 {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "notes required"}
	}

	var totalIn uint64
	for _, n := range notes {
		var ok bool
		totalIn, ok = addUint64(totalIn, n.ValueZat)
		if !ok {
			return types.TxPlan{}, 0, errors.New("txplan: notes sum overflow")
		}
	}

	fee, err := RequiredFeeSend(len(notes), 1)
	if err != nil {
		return types.TxPlan{}, 0, err
	}
	if totalIn <= fee {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeNoLiquidityInHot, Message: "insufficient funds"}
	}
	amount := totalIn - fee

	positions := make([]uint32, 0, len(notes))
	planNotes := make([]types.OrchardSpendNote, 0, len(notes))
	for _, n := range notes {
		act, err := orchardActionForNote(ctx, cfg.RPC, blockCache, n.Height, n.TxID, n.ActionIndex)
		if err != nil {
			return types.TxPlan{}, 0, err
		}
		positions = append(positions, n.Position)
		planNotes = append(planNotes, types.OrchardSpendNote{
			NoteID:          n.TxID + ":" + strconv.FormatUint(uint64(n.ActionIndex), 10),
			ActionNullifier: act.Nullifier,
			CMX:             act.CMX,
			Position:        n.Position,
			Path:            nil,
			EphemeralKey:    act.EphemeralKey,
			EncCiphertext:   act.EncCiphertext,
		})
	}

	wit, err := cfg.Scan.OrchardWitness(ctx, nil, positions)
	if err != nil {
		return types.TxPlan{}, 0, err
	}
	if strings.TrimSpace(wit.Root) == "" || len(wit.Paths) != len(positions) {
		return types.TxPlan{}, 0, errors.New("txplan: invalid witness response")
	}
	if wit.AnchorHeight < 0 || wit.AnchorHeight > int64(^uint32(0)) {
		return types.TxPlan{}, 0, errors.New("txplan: invalid witness anchor_height")
	}
	pathByPos := make(map[uint32][]string, len(wit.Paths))
	for _, p := range wit.Paths {
		pathByPos[p.Position] = p.AuthPath
	}
	for i := range planNotes {
		p, ok := pathByPos[planNotes[i].Position]
		if !ok || len(p) != 32 {
			return types.TxPlan{}, 0, errors.New("txplan: witness path missing")
		}
		planNotes[i].Path = p
	}

	plan := types.TxPlan{
		Version:       types.V0,
		Kind:          types.TxPlanKindSweep,
		WalletID:      cfg.Wallet,
		CoinType:      coinType,
		Account:       cfg.Account,
		Chain:         chainInfo.Chain,
		BranchID:      chainInfo.BranchID,
		AnchorHeight:  uint32(wit.AnchorHeight),
		Anchor:        wit.Root,
		ExpiryHeight:  expiryHeight,
		Outputs:       []types.TxOutput{{ToAddress: toAddress, AmountZat: strconv.FormatUint(amount, 10), MemoHex: memoHex}},
		ChangeAddress: changeAddress,
		FeeZat:        strconv.FormatUint(fee, 10),
		Notes:         planNotes,
	}
	return plan, fee, nil
}
