package txplan

import "testing"

func TestSplitNotesBalanced(t *testing.T) {
	mk := func(n int) []spendableNote {
		out := make([]spendableNote, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, spendableNote{
				TxID:        "tx" + itoa(i),
				ActionIndex: uint32(i),
				Height:      1,
				Position:    uint32(i),
				ValueZat:    uint64(10_000 + i),
			})
		}
		return out
	}

	one := splitNotesBalanced(mk(10), 200)
	if len(one) != 1 || len(one[0]) != 10 {
		t.Fatalf("unexpected chunks: %+v", lens(one))
	}

	two := splitNotesBalanced(mk(201), 200)
	if len(two) != 2 {
		t.Fatalf("chunks=%d want %d", len(two), 2)
	}
	if len(two[0]) != 101 || len(two[1]) != 100 {
		t.Fatalf("unexpected chunk sizes: %+v", lens(two))
	}

	three := splitNotesBalanced(mk(401), 200)
	if len(three) != 3 {
		t.Fatalf("chunks=%d want %d", len(three), 3)
	}
	if len(three[0]) < 133 || len(three[1]) < 133 || len(three[2]) < 133 {
		t.Fatalf("unexpected chunk sizes: %+v", lens(three))
	}
}

func lens(chunks [][]spendableNote) []int {
	out := make([]int, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, len(c))
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + (n % 10))
		n /= 10
	}
	return string(b[i:])
}

