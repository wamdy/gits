package repo

import (
	"testing"

	"github.com/wamdy/gits/internal/config"
)

func TestFindReposByWildcard(t *testing.T) {
	repos := []config.Repository{
		{ID: 1, Name: "repo-backend"},
		{ID: 2, Name: "repo-frontend"},
		{ID: 3, Name: "genealogy-web"},
		{ID: 4, Name: "genealogy-api"},
	}

	tests := []struct {
		name    string
		pattern string
		want    []int // IDs
	}{
		{"prefix star", "repo*", []int{1, 2}},
		{"suffix star", "*api", []int{4}},
		{"question mark", "repo-????end", []int{1}},
		{"exact", "genealogy-web", []int{3}},
		{"no match", "xxxx*", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FindReposByWildcard(tt.pattern, repos)
			if len(got) != len(tt.want) {
				t.Errorf("FindReposByWildcard(%q) = %d repos, want %d", tt.pattern, len(got), len(tt.want))
				return
			}
			for i, r := range got {
				if r.ID != tt.want[i] {
					t.Errorf("FindReposByWildcard(%q)[%d] = ID %d, want %d", tt.pattern, i, r.ID, tt.want[i])
				}
			}
		})
	}
}

func TestResolveSelection(t *testing.T) {
	repos := []config.Repository{
		{ID: 1, Name: "repo-backend"},
		{ID: 2, Name: "repo-frontend"},
		{ID: 3, Name: "genealogy-web"},
		{ID: 4, Name: "genealogy-api"},
		{ID: 5, Name: "tools"},
	}

	tests := []struct {
		name    string
		input   string
		want    []int // IDs
		wantErr bool
	}{
		{"pure IDs", "1,3,5", []int{1, 3, 5}, false},
		{"ID range", "2-4", []int{2, 3, 4}, false},
		{"wildcard", "repo*", []int{1, 2}, false},
		{"name exact", "tools", []int{5}, false},
		{"mixed", "1,repo*", []int{1, 2}, false},
		{"mixed with range", "1-2,genealogy*", []int{1, 2, 3, 4}, false},
		{"no match", "xxxx*", nil, false},
		{"dedup", "1,1,1-2", []int{1, 2}, false},
		{"invalid range", "5-3", nil, true},
		{"invalid range format", "1-5-8", nil, true},
		{"empty segment ignored", "1,,2", []int{1, 2}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSelection(tt.input, repos)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ResolveSelection(%q) expected error", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("ResolveSelection(%q) unexpected error: %v", tt.input, err)
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("ResolveSelection(%q) = %d repos, want %d", tt.input, len(got), len(tt.want))
				return
			}
			for i, r := range got {
				if r.ID != tt.want[i] {
					t.Errorf("ResolveSelection(%q)[%d] = ID %d, want %d", tt.input, i, r.ID, tt.want[i])
				}
			}
		})
	}
}

func TestLooksLikeIDRange(t *testing.T) {
	tests := []struct {
		seg  string
		want bool
	}{
		{"1", true},
		{"1-5", true},
		{"1-5-8", true},
		{"repo-backend", false},
		{"repo*", false},
		{"abc", false},
		{"-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.seg, func(t *testing.T) {
			if got := looksLikeIDRange(tt.seg); got != tt.want {
				t.Errorf("looksLikeIDRange(%q) = %v, want %v", tt.seg, got, tt.want)
			}
		})
	}
}