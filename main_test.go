package main

import (
	"testing"
	"time"
)

func ts(d time.Duration) string {
	return time.Now().Add(-d).UTC().Format(time.RFC3339)
}

func TestPickVictim(t *testing.T) {
	day := 24 * time.Hour

	tests := []struct {
		name   string
		counts map[string]int
		files  []fileEntry
		want   string
	}{
		{
			name:   "least downloaded among eligible (>7d) wins",
			counts: map[string]int{"a": 100, "b": 3, "c": 50},
			files: []fileEntry{
				{Name: "a", MTime: ts(10 * day)},
				{Name: "b", MTime: ts(8 * day)},
				{Name: "c", MTime: ts(9 * day)},
			},
			want: "b",
		},
		{
			name:   "recent files (<7d) are protected even with 0 downloads",
			counts: map[string]int{"old": 5, "fresh": 0},
			files: []fileEntry{
				{Name: "old", MTime: ts(9 * day)},
				{Name: "fresh", MTime: ts(1 * day)},
			},
			want: "old",
		},
		{
			name:   "tie on downloads -> oldest eligible wins",
			counts: map[string]int{"x": 2, "y": 2, "z": 2},
			files: []fileEntry{
				{Name: "x", MTime: ts(8 * day)},
				{Name: "y", MTime: ts(20 * day)},
				{Name: "z", MTime: ts(12 * day)},
			},
			want: "y",
		},
		{
			name:   "no file old enough -> fall back to oldest overall",
			counts: map[string]int{"p": 0, "q": 99},
			files: []fileEntry{
				{Name: "p", MTime: ts(2 * day)},
				{Name: "q", MTime: ts(5 * day)},
			},
			want: "q",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &server{counts: tt.counts}
			got := s.pickVictim(tt.files)
			if got.Name != tt.want {
				t.Fatalf("pickVictim = %q, want %q", got.Name, tt.want)
			}
		})
	}
}
