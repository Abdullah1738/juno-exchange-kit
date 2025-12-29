package txplan

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Abdullah1738/juno-exchange-kit/internal/chain"
	"github.com/Abdullah1738/juno-sdk-go/junocashd"
	"github.com/Abdullah1738/juno-sdk-go/junoscan"
	"github.com/Abdullah1738/juno-sdk-go/types"
)

type Output struct {
	ToAddress string
	AmountZat uint64
	MemoHex   string
}

type SendConfig struct {
	RPC      *junocashd.Client
	Scan     *junoscan.Client
	Wallet   string
	CoinType uint32
	Account  uint32

	MinConfirmations int64
	ExpiryOffset     uint32
}

func PlanWithdrawal(ctx context.Context, cfg SendConfig, outputs []Output, changeAddress string) (types.TxPlan, uint64, error) {
	return planSend(ctx, cfg, types.TxPlanKindWithdrawal, outputs, changeAddress)
}

func PlanRebalance(ctx context.Context, cfg SendConfig, outputs []Output, changeAddress string) (types.TxPlan, uint64, error) {
	return planSend(ctx, cfg, types.TxPlanKindRebalance, outputs, changeAddress)
}

func PlanSweep(ctx context.Context, cfg SendConfig, toAddress string, memoHex string, changeAddress string) (types.TxPlan, uint64, error) {
	if cfg.RPC == nil || cfg.Scan == nil {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "rpc/scan required"}
	}
	cfg.Wallet = strings.TrimSpace(cfg.Wallet)
	toAddress = strings.TrimSpace(toAddress)
	memoHex = strings.TrimSpace(memoHex)
	changeAddress = strings.TrimSpace(changeAddress)
	if cfg.Wallet == "" {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "wallet required"}
	}
	if toAddress == "" {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "to_address required"}
	}
	if changeAddress == "" {
		changeAddress = toAddress
	}
	if cfg.MinConfirmations <= 0 {
		cfg.MinConfirmations = 1
	}
	if cfg.ExpiryOffset == 0 {
		cfg.ExpiryOffset = 40
	}

	chainInfo, err := chain.GetInfo(ctx, cfg.RPC)
	if err != nil {
		return types.TxPlan{}, 0, err
	}
	coinType := cfg.CoinType
	if coinType == 0 {
		switch strings.ToLower(strings.TrimSpace(chainInfo.Chain)) {
		case "main":
			coinType = 8133
		case "test":
			coinType = 8134
		case "regtest":
			coinType = 8135
		default:
			return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "unknown chain"}
		}
	}
	if chainInfo.Height < 0 || chainInfo.Height > int64(^uint32(0)) {
		return types.TxPlan{}, 0, errors.New("txplan: invalid chain height")
	}
	expiryHeight := uint32(chainInfo.Height) + cfg.ExpiryOffset
	if expiryHeight < uint32(chainInfo.Height) {
		return types.TxPlan{}, 0, errors.New("txplan: expiry height overflow")
	}

	notes, err := listSpendableNotes(ctx, cfg.Scan, cfg.Wallet, chainInfo.Height, cfg.MinConfirmations)
	if err != nil {
		return types.TxPlan{}, 0, err
	}
	if len(notes) == 0 {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeNoLiquidityInHot, Message: "no spendable notes"}
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
	blockCache := make(map[int64]blockV2)
	for _, n := range notes {
		act, err := orchardActionForNote(ctx, cfg.RPC, blockCache, n.Height, n.TxID, n.ActionIndex)
		if err != nil {
			return types.TxPlan{}, 0, err
		}
		positions = append(positions, n.Position)
		planNotes = append(planNotes, types.OrchardSpendNote{
			NoteID:          fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex),
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

func planSend(ctx context.Context, cfg SendConfig, kind types.TxPlanKind, outputs []Output, changeAddress string) (types.TxPlan, uint64, error) {
	if cfg.RPC == nil || cfg.Scan == nil {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "rpc/scan required"}
	}
	cfg.Wallet = strings.TrimSpace(cfg.Wallet)
	changeAddress = strings.TrimSpace(changeAddress)
	if cfg.Wallet == "" {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "wallet required"}
	}
	if changeAddress == "" {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "change_address required"}
	}
	if len(outputs) == 0 {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "outputs required"}
	}
	for i := range outputs {
		outputs[i].ToAddress = strings.TrimSpace(outputs[i].ToAddress)
		outputs[i].MemoHex = strings.TrimSpace(outputs[i].MemoHex)
		if outputs[i].ToAddress == "" || outputs[i].AmountZat == 0 {
			return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: fmt.Sprintf("outputs[%d] invalid", i)}
		}
	}
	if cfg.MinConfirmations <= 0 {
		cfg.MinConfirmations = 1
	}
	if cfg.ExpiryOffset == 0 {
		cfg.ExpiryOffset = 40
	}

	chainInfo, err := chain.GetInfo(ctx, cfg.RPC)
	if err != nil {
		return types.TxPlan{}, 0, err
	}

	coinType := cfg.CoinType
	if coinType == 0 {
		switch strings.ToLower(strings.TrimSpace(chainInfo.Chain)) {
		case "main":
			coinType = 8133
		case "test":
			coinType = 8134
		case "regtest":
			coinType = 8135
		default:
			return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "unknown chain"}
		}
	}

	if chainInfo.Height < 0 || chainInfo.Height > int64(^uint32(0)) {
		return types.TxPlan{}, 0, errors.New("txplan: invalid chain height")
	}
	expiryHeight := uint32(chainInfo.Height) + cfg.ExpiryOffset
	if expiryHeight < uint32(chainInfo.Height) {
		return types.TxPlan{}, 0, errors.New("txplan: expiry height overflow")
	}

	notes, err := listSpendableNotes(ctx, cfg.Scan, cfg.Wallet, chainInfo.Height, cfg.MinConfirmations)
	if err != nil {
		return types.TxPlan{}, 0, err
	}
	if len(notes) == 0 {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeNoLiquidityInHot, Message: "no spendable notes"}
	}

	var totalOut uint64
	for _, o := range outputs {
		var ok bool
		totalOut, ok = addUint64(totalOut, o.AmountZat)
		if !ok {
			return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeInvalidRequest, Message: "outputs sum overflow"}
		}
	}

	selected, fee, err := SelectNotes(notesToUnspent(notes), totalOut, len(outputs))
	if err != nil {
		return types.TxPlan{}, 0, types.CodedError{Code: types.ErrCodeNoLiquidityInHot, Message: "insufficient funds"}
	}

	// Fetch actions + witness.
	positions := make([]uint32, 0, len(selected))
	planNotes := make([]types.OrchardSpendNote, 0, len(selected))

	blockCache := make(map[int64]blockV2)
	for _, sel := range selected {
		n := findNote(notes, sel.TxID, sel.ActionIndex)
		if n == nil {
			return types.TxPlan{}, 0, errors.New("txplan: selected note missing")
		}

		act, err := orchardActionForNote(ctx, cfg.RPC, blockCache, n.Height, n.TxID, n.ActionIndex)
		if err != nil {
			return types.TxPlan{}, 0, err
		}

		positions = append(positions, n.Position)
		planNotes = append(planNotes, types.OrchardSpendNote{
			NoteID:          fmt.Sprintf("%s:%d", n.TxID, n.ActionIndex),
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

	outOutputs := make([]types.TxOutput, 0, len(outputs))
	for _, o := range outputs {
		outOutputs = append(outOutputs, types.TxOutput{
			ToAddress: o.ToAddress,
			AmountZat: strconv.FormatUint(o.AmountZat, 10),
			MemoHex:   o.MemoHex,
		})
	}

	plan := types.TxPlan{
		Version:       types.V0,
		Kind:          kind,
		WalletID:      cfg.Wallet,
		CoinType:      coinType,
		Account:       cfg.Account,
		Chain:         chainInfo.Chain,
		BranchID:      chainInfo.BranchID,
		AnchorHeight:  uint32(wit.AnchorHeight),
		Anchor:        wit.Root,
		ExpiryHeight:  expiryHeight,
		Outputs:       outOutputs,
		ChangeAddress: changeAddress,
		FeeZat:        strconv.FormatUint(fee, 10),
		Notes:         planNotes,
	}
	return plan, fee, nil
}

type spendableNote struct {
	TxID        string
	ActionIndex uint32
	Height      int64
	Position    uint32
	ValueZat    uint64
}

func listSpendableNotes(ctx context.Context, sc *junoscan.Client, walletID string, tipHeight int64, minConf int64) ([]spendableNote, error) {
	raw, err := sc.ListWalletNotes(ctx, walletID, true)
	if err != nil {
		return nil, err
	}
	out := make([]spendableNote, 0, len(raw))
	for _, n := range raw {
		if n.Position == nil || *n.Position < 0 {
			continue
		}
		if n.Height < 0 {
			continue
		}
		if tipHeight < n.Height {
			continue
		}
		conf := tipHeight - n.Height + 1
		if conf < minConf {
			continue
		}
		if n.ActionIndex < 0 {
			continue
		}
		if n.ValueZat <= 0 {
			continue
		}
		if n.ValueZat > int64(^uint64(0)>>1) {
			return nil, errors.New("txplan: note value too large")
		}
		if *n.Position > int64(^uint32(0)) {
			return nil, errors.New("txplan: note position too large")
		}
		out = append(out, spendableNote{
			TxID:        strings.ToLower(strings.TrimSpace(n.TxID)),
			ActionIndex: uint32(n.ActionIndex),
			Height:      n.Height,
			Position:    uint32(*n.Position),
			ValueZat:    uint64(n.ValueZat),
		})
	}
	return out, nil
}

func notesToUnspent(ns []spendableNote) []UnspentNote {
	out := make([]UnspentNote, 0, len(ns))
	for _, n := range ns {
		out = append(out, UnspentNote{TxID: n.TxID, ActionIndex: n.ActionIndex, ValueZat: n.ValueZat})
	}
	return out
}

func findNote(ns []spendableNote, txid string, actionIndex uint32) *spendableNote {
	for i := range ns {
		if ns[i].TxID == txid && ns[i].ActionIndex == actionIndex {
			return &ns[i]
		}
	}
	return nil
}

type orchardAction struct {
	Nullifier     string
	CMX           string
	EphemeralKey  string
	EncCiphertext string
}

type blockV2 struct {
	Tx []struct {
		TxID    string `json:"txid"`
		Orchard struct {
			Actions []struct {
				Nullifier     string `json:"nullifier"`
				CMX           string `json:"cmx"`
				EphemeralKey  string `json:"ephemeralKey"`
				EncCiphertext string `json:"encCiphertext"`
			} `json:"actions"`
		} `json:"orchard"`
	} `json:"tx"`
}

func orchardActionForNote(ctx context.Context, rpc *junocashd.Client, cache map[int64]blockV2, height int64, txid string, actionIndex uint32) (orchardAction, error) {
	blk, ok := cache[height]
	if !ok {
		hash, err := rpc.GetBlockHash(ctx, height)
		if err != nil {
			return orchardAction{}, err
		}
		if err := rpc.Call(ctx, "getblock", []any{hash, 2}, &blk); err != nil {
			return orchardAction{}, err
		}
		cache[height] = blk
	}

	txid = strings.ToLower(strings.TrimSpace(txid))
	for _, t := range blk.Tx {
		if strings.ToLower(strings.TrimSpace(t.TxID)) != txid {
			continue
		}
		if int(actionIndex) < 0 || int(actionIndex) >= len(t.Orchard.Actions) {
			return orchardAction{}, errors.New("txplan: action_index out of range")
		}
		a := t.Orchard.Actions[actionIndex]
		act := orchardAction{
			Nullifier:     strings.ToLower(strings.TrimSpace(a.Nullifier)),
			CMX:           strings.ToLower(strings.TrimSpace(a.CMX)),
			EphemeralKey:  strings.ToLower(strings.TrimSpace(a.EphemeralKey)),
			EncCiphertext: strings.ToLower(strings.TrimSpace(a.EncCiphertext)),
		}
		if len(act.EncCiphertext) >= 104 {
			act.EncCiphertext = act.EncCiphertext[:104]
		}
		if !is32ByteHex(act.Nullifier) || !is32ByteHex(act.CMX) || !is32ByteHex(act.EphemeralKey) {
			return orchardAction{}, errors.New("txplan: invalid orchard action encoding")
		}
		if len(act.EncCiphertext) != 104 {
			return orchardAction{}, errors.New("txplan: invalid orchard action encoding")
		}
		if _, err := hex.DecodeString(act.EncCiphertext); err != nil {
			return orchardAction{}, errors.New("txplan: invalid orchard action encoding")
		}
		return act, nil
	}

	return orchardAction{}, errors.New("txplan: tx not found in block")
}

func is32ByteHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
