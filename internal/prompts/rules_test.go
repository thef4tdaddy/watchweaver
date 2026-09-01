package prompts

import (
	"reflect"
	"testing"
)

func TestEvaluateRules(t *testing.T) {
	tests := []struct {
		name string
		in   Batch
		want []Decision
	}{
		{name: "baseline silent", in: Batch{Baseline: true, NewMovieWatches: []int64{1}}, want: nil},
		{name: "movie watch", in: Batch{NewMovieWatches: []int64{1}}, want: []Decision{{Kind: MovieRating, MediaID: 1}}},
		{name: "movie rewatch remains eligible", in: Batch{NewMovieWatches: []int64{1, 1}}, want: []Decision{{Kind: MovieRating, MediaID: 1}, {Kind: MovieRating, MediaID: 1}}},
		{name: "ignored movie silent", in: Batch{NewMovieWatches: []int64{1}, IgnoredMovieIDs: map[int64]bool{1: true}}, want: nil},
		{name: "weekly caught up", in: Batch{NewEpisodeIDs: []int64{2}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: true, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Watched: true, Normal: true}, {ID: 3, Number: 3, Released: false, Normal: true}}}}}, want: []Decision{{Kind: EpisodeRating, MediaID: 2}}},
		{name: "binge backlog silent", in: Batch{NewEpisodeIDs: []int64{1}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: true, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Normal: true}, {ID: 3, Number: 3, Released: true, Normal: true}}}}}, want: nil},
		{name: "full season batch emits only season", in: Batch{NewEpisodeIDs: []int64{1, 2, 3}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: true, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Watched: true, Normal: true}, {ID: 3, Number: 3, Released: true, Watched: true, Normal: true}}}}}, want: []Decision{{Kind: SeasonRating, MediaID: 10}}},
		{name: "special does not block season", in: Batch{NewEpisodeIDs: []int64{2}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: true, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Watched: true, Normal: true}, {ID: 99, Number: 0, Released: true, Normal: false}}}}}, want: []Decision{{Kind: SeasonRating, MediaID: 10}}},
		{name: "split season caught up", in: Batch{NewEpisodeIDs: []int64{4}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: true, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Watched: true, Normal: true}, {ID: 3, Number: 3, Released: true, Watched: true, Normal: true}, {ID: 4, Number: 4, Released: true, Watched: true, Normal: true}, {ID: 5, Number: 5, Released: false, Normal: true}}}}}, want: []Decision{{Kind: EpisodeRating, MediaID: 4}}},
		{name: "ended inventory completes season", in: Batch{NewEpisodeIDs: []int64{2}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: true, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Watched: true, Normal: true}}}}}, want: []Decision{{Kind: SeasonRating, MediaID: 10}}},
		{name: "out of order blocks", in: Batch{NewEpisodeIDs: []int64{3}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: true, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Normal: true}, {ID: 3, Number: 3, Released: true, Watched: true, Normal: true}, {ID: 4, Number: 4, Released: false, Normal: true}}}}}, want: nil},
		{name: "settled batch suppresses obsolete episode", in: Batch{NewEpisodeIDs: []int64{2, 3}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: true, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Watched: true, Normal: true}, {ID: 3, Number: 3, Released: true, Watched: true, Normal: true}}}}}, want: []Decision{{Kind: SeasonRating, MediaID: 10}}},
		{name: "uncertain metadata silent", in: Batch{NewEpisodeIDs: []int64{2}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: false, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Watched: true, Normal: true}}}}}, want: nil},
		{name: "ignored show silent", in: Batch{NewEpisodeIDs: []int64{2}, IgnoredShowIDs: map[int64]bool{100: true}, Seasons: []SeasonState{{SeasonID: 10, ShowID: 100, InventoryKnown: true, Episodes: []Episode{{ID: 1, Number: 1, Released: true, Watched: true, Normal: true}, {ID: 2, Number: 2, Released: true, Watched: true, Normal: true}}}}}, want: nil},
		{name: "existing rating does not alter eligibility", in: Batch{NewMovieWatches: []int64{7}}, want: []Decision{{Kind: MovieRating, MediaID: 7}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Evaluate(test.in)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Evaluate() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestEvaluateDeterministic(t *testing.T) {
	in := Batch{
		NewMovieWatches: []int64{9, 3},
		NewEpisodeIDs:   []int64{22},
		Seasons: []SeasonState{{SeasonID: 20, ShowID: 2, InventoryKnown: true, Episodes: []Episode{
			{ID: 21, Number: 1, Released: true, Watched: true, Normal: true},
			{ID: 22, Number: 2, Released: true, Watched: true, Normal: true},
			{ID: 23, Number: 3, Released: false, Normal: true},
		}}},
	}
	first := Evaluate(in)
	second := Evaluate(in)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic results: %#v != %#v", first, second)
	}
}
