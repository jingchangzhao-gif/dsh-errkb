package core

// Jaccard returns the Jaccard similarity of two token lists: the number of
// shared tokens divided by the number of distinct tokens in either list.
//
// The result is a number from 0 to 1, or 0 when both lists are empty: two empty
// messages give nothing to compare, which matches the TypeScript engine.
// Duplicates count once, so lists from Tokenize are used as they are, and
// unsorted lists work too.
//
// The function only ranks. It deliberately returns no boolean "merge" verdict:
// near matches are candidates (EKB-017), and a merge needs a person to confirm
// it (EKB-024, §5).
func Jaccard(a, b []string) float64 {
	setA := make(map[string]struct{}, len(a))
	for _, token := range a {
		setA[token] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, token := range b {
		setB[token] = struct{}{}
	}

	shared := 0
	for token := range setA {
		if _, ok := setB[token]; ok {
			shared++
		}
	}
	union := len(setA) + len(setB) - shared
	if union == 0 {
		return 0
	}
	return float64(shared) / float64(union)
}
