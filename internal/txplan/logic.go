package txplan

import (
	"errors"
	"sort"
)

type UnspentNote struct {
	TxID        string
	ActionIndex uint32
	ValueZat    uint64
}

func RequiredFeeSend(spendCount, outputCount int) (uint64, error) {
	actions := spendCount
	if outputCount > actions {
		actions = outputCount
	}
	if actions < 2 {
		actions = 2
	}
	fee := uint64(5_000) * uint64(actions)
	if fee < 5_000 {
		return 0, errors.New("fee overflow")
	}
	return fee, nil
}

func SelectNotes(notes []UnspentNote, amountZat uint64, outputCount int) ([]UnspentNote, uint64, error) {
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].ValueZat != notes[j].ValueZat {
			return notes[i].ValueZat > notes[j].ValueZat
		}
		if notes[i].TxID != notes[j].TxID {
			return notes[i].TxID < notes[j].TxID
		}
		return notes[i].ActionIndex < notes[j].ActionIndex
	})

	var selected []UnspentNote
	var total uint64
	for _, n := range notes {
		selected = append(selected, n)
		total += n.ValueZat

		feeWithChange, err := RequiredFeeSend(len(selected), outputCount+1)
		if err != nil {
			return nil, 0, err
		}
		need, ok := addUint64(amountZat, feeWithChange)
		if !ok {
			return nil, 0, errors.New("overflow")
		}
		if total > need {
			return selected, feeWithChange, nil
		}
		if total == need {
			feeNoChange, err := RequiredFeeSend(len(selected), outputCount)
			if err != nil {
				return nil, 0, err
			}
			if feeNoChange == feeWithChange {
				return selected, feeWithChange, nil
			}
		}
	}
	return nil, 0, errors.New("insufficient funds")
}

func addUint64(a, b uint64) (uint64, bool) {
	sum := a + b
	if sum < a {
		return 0, false
	}
	return sum, true
}
