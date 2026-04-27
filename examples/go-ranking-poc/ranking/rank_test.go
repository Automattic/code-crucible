package ranking

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestTopN(t *testing.T) {
	tests := []struct {
		name   string
		scores []int
		n      int
		want   []int
	}{
		{
			name:   "basic",
			scores: []int{4, 9, 1, 7, 3},
			n:      3,
			want:   []int{9, 7, 4},
		},
		{
			name:   "duplicates",
			scores: []int{5, 5, 2, 9, 9, 1},
			n:      4,
			want:   []int{9, 9, 5, 5},
		},
		{
			name:   "negative scores",
			scores: []int{-5, -1, -3, -2},
			n:      2,
			want:   []int{-1, -2},
		},
		{
			name:   "n exceeds length",
			scores: []int{2, 1},
			n:      5,
			want:   []int{2, 1},
		},
		{
			name:   "zero n",
			scores: []int{2, 1},
			n:      0,
			want:   []int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := append([]int(nil), tt.scores...)
			got := TopN(input, tt.n)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("TopN() = %#v, want %#v", got, tt.want)
			}
			if !reflect.DeepEqual(input, tt.scores) {
				t.Fatalf("TopN mutated input: got %#v, want %#v", input, tt.scores)
			}
		})
	}
}

func BenchmarkTopN(b *testing.B) {
	scores := make([]int, 4096)
	rng := rand.New(rand.NewSource(42))
	for i := range scores {
		scores[i] = rng.Intn(1_000_000) - 500_000
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got := TopN(scores, 25)
		if len(got) != 25 {
			b.Fatalf("TopN returned %d values", len(got))
		}
	}
}
