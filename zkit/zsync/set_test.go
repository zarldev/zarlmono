package zsync_test

import (
	"cmp"
	"slices"
	"sync"
	"testing"

	"github.com/zarldev/zarlmono/zkit/zsync"
)

func TestSet_Add(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   int
	}{
		{
			name:   "add single value",
			values: []string{"test"},
			want:   1,
		},
		{
			name:   "add multiple values",
			values: []string{"a", "b", "c"},
			want:   3,
		},
		{
			name:   "add duplicate values",
			values: []string{"test", "test", "other"},
			want:   2,
		},
		{
			name:   "add empty string",
			values: []string{""},
			want:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := zsync.NewSet[string]()

			for _, v := range tt.values {
				s.Add(v)
			}

			if got := s.Len(); got != tt.want {
				t.Errorf("Len() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_Contains(t *testing.T) {
	tests := []struct {
		name  string
		setup []string
		value string
		want  bool
	}{
		{
			name:  "contains existing value",
			setup: []string{"a", "b", "c"},
			value: "b",
			want:  true,
		},
		{
			name:  "does not contain missing value",
			setup: []string{"a", "b", "c"},
			value: "d",
			want:  false,
		},
		{
			name:  "empty set",
			setup: []string{},
			value: "any",
			want:  false,
		},
		{
			name:  "contains empty string",
			setup: []string{"", "test"},
			value: "",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := zsync.NewSet[string]()

			for _, v := range tt.setup {
				s.Add(v)
			}

			if got := s.Contains(tt.value); got != tt.want {
				t.Errorf("Contains() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_Remove(t *testing.T) {
	tests := []struct {
		name   string
		setup  []string
		value  string
		want   bool
		length int
	}{
		{
			name:   "remove existing value",
			setup:  []string{"a", "b", "c"},
			value:  "b",
			want:   true,
			length: 2,
		},
		{
			name:   "remove non-existent value",
			setup:  []string{"a", "b", "c"},
			value:  "d",
			want:   false,
			length: 3,
		},
		{
			name:   "remove from empty set",
			setup:  []string{},
			value:  "any",
			want:   false,
			length: 0,
		},
		{
			name:   "remove last value",
			setup:  []string{"only"},
			value:  "only",
			want:   true,
			length: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := zsync.NewSet[string]()

			for _, v := range tt.setup {
				s.Add(v)
			}

			got := s.Remove(tt.value)
			if got != tt.want {
				t.Errorf("Remove() = %v, want %v", got, tt.want)
			}

			if s.Len() != tt.length {
				t.Errorf("Len() after remove = %v, want %v", s.Len(), tt.length)
			}
		})
	}
}

func TestSet_Len(t *testing.T) {
	tests := []struct {
		name  string
		setup []string
		want  int
	}{
		{
			name:  "empty set",
			setup: []string{},
			want:  0,
		},
		{
			name:  "single value",
			setup: []string{"test"},
			want:  1,
		},
		{
			name:  "multiple values",
			setup: []string{"a", "b", "c"},
			want:  3,
		},
		{
			name:  "duplicates ignored",
			setup: []string{"a", "a", "b"},
			want:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := zsync.NewSet[string]()

			for _, v := range tt.setup {
				s.Add(v)
			}

			if got := s.Len(); got != tt.want {
				t.Errorf("Len() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_Values(t *testing.T) {
	tests := []struct {
		name  string
		setup []string
		want  []string
	}{
		{
			name:  "empty set",
			setup: []string{},
			want:  []string{},
		},
		{
			name:  "single value",
			setup: []string{"test"},
			want:  []string{"test"},
		},
		{
			name:  "multiple values",
			setup: []string{"a", "b", "c"},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "duplicates removed",
			setup: []string{"a", "a", "b"},
			want:  []string{"a", "b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := zsync.NewSet[string]()

			for _, v := range tt.setup {
				s.Add(v)
			}

			got := s.Values()
			if len(got) != len(tt.want) {
				t.Errorf("Values() length = %v, want %v", len(got), len(tt.want))
				return
			}

			// unordered comparison
			gotMap := make(map[string]bool)
			for _, v := range got {
				gotMap[v] = true
			}

			for _, wantValue := range tt.want {
				if !gotMap[wantValue] {
					t.Errorf("Values() missing value %v", wantValue)
				}
			}
		})
	}
}

func TestSet_Clear(t *testing.T) {
	s := zsync.NewSet[string]()
	s.Add("a")
	s.Add("b")
	s.Add("c")

	if s.Len() != 3 {
		t.Errorf("Len() before clear = %v, want 3", s.Len())
	}

	s.Clear()

	if s.Len() != 0 {
		t.Errorf("Len() after clear = %v, want 0", s.Len())
	}

	if s.Contains("a") {
		t.Errorf("Contains() after clear = true, want false")
	}
}

func TestSet_Ordered(t *testing.T) {
	tests := []struct {
		name  string
		setup []string
		want  []string
	}{
		{
			name:  "empty set",
			setup: []string{},
			want:  []string{},
		},
		{
			name:  "single value",
			setup: []string{"test"},
			want:  []string{"test"},
		},
		{
			name:  "multiple values",
			setup: []string{"c", "a", "b"},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "already sorted",
			setup: []string{"a", "b", "c"},
			want:  []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := zsync.NewSet[string]()

			for _, v := range tt.setup {
				s.Add(v)
			}

			got := s.Ordered(cmp.Compare[string])
			if !slices.Equal(got, tt.want) {
				t.Errorf("OrderedValues() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSet_OrderedNumbers(t *testing.T) {
	tests := []struct {
		name  string
		setup []int
		want  []int
	}{
		{
			name:  "empty set",
			setup: []int{},
			want:  []int{},
		},
		{
			name:  "single value",
			setup: []int{1},
			want:  []int{1},
		},
		{
			name:  "multiple values",
			setup: []int{3, 2, 1},
			want:  []int{1, 2, 3},
		},
		{
			name:  "already sorted",
			setup: []int{1, 2, 3},
			want:  []int{1, 2, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := zsync.NewSet[int]()

			for _, v := range tt.setup {
				s.Add(v)
			}

			got := s.Ordered(cmp.Compare[int])
			if !slices.Equal(got, tt.want) {
				t.Errorf("Ordered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrdered(t *testing.T) {
	tests := []struct {
		name  string
		setup []string
		want  []string
	}{
		{
			name:  "empty set",
			setup: []string{},
			want:  []string{},
		},
		{
			name:  "single value",
			setup: []string{"test"},
			want:  []string{"test"},
		},
		{
			name:  "multiple values",
			setup: []string{"c", "a", "b"},
			want:  []string{"a", "b", "c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := zsync.NewSet[string]()

			for _, v := range tt.setup {
				s.Add(v)
			}

			got := zsync.Ordered(s)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Ordered() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrdered_Numbers(t *testing.T) {
	s := zsync.NewSet[int]()
	s.Add(3)
	s.Add(1)
	s.Add(4)
	s.Add(1) // duplicate
	s.Add(5)

	got := zsync.Ordered(s)
	want := []int{1, 3, 4, 5}

	if !slices.Equal(got, want) {
		t.Errorf("Ordered() = %v, want %v", got, want)
	}
}

// concurrent access test.
func TestSet_Concurrent(t *testing.T) {
	s := zsync.NewSet[int]()

	const numGoroutines = 50
	const numOperations = 100

	var wg sync.WaitGroup

	// concurrent adds
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range numOperations {
				value := id*numOperations + j
				s.Add(value)
			}
		}(i)
	}

	// concurrent contains checks
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range numOperations {
				value := id*numOperations + j
				s.Contains(value) // ignore result, just testing for races
			}
		}(i)
	}

	// concurrent removes
	for i := range numGoroutines / 2 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range numOperations / 2 {
				value := id*numOperations + j
				s.Remove(value)
			}
		}(i)
	}

	wg.Wait()

	// ensure no races or corruption
	length := s.Len()
	values := s.Values()

	if len(values) != length {
		t.Errorf("Values() length %v != Len() %v", len(values), length)
	}
}
