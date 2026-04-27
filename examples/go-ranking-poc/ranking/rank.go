package ranking

// TopN returns the n highest scores in descending order without modifying the input.
func TopN(scores []int, n int) []int {
	if n <= 0 {
		return []int{}
	}

	copied := make([]int, len(scores))
	for i := 0; i < len(scores); i++ {
		copied[i] = scores[i]
	}

	for i := 0; i < len(copied); i++ {
		for j := 0; j < len(copied)-1; j++ {
			if copied[j] < copied[j+1] {
				copied[j], copied[j+1] = copied[j+1], copied[j]
			}
		}
	}

	if n > len(copied) {
		n = len(copied)
	}

	out := make([]int, n)
	for i := 0; i < n; i++ {
		out[i] = copied[i]
	}
	return out
}
