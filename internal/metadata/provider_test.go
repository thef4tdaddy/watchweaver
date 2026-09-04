package metadata

import "testing"

func TestFinaleFromTrakt(t *testing.T) {
	tests := map[string]struct {
		want      FinaleType
		completes bool
	}{
		"episode":           {FinaleNone, false},
		"mid_season_finale": {FinaleMidSeason, false},
		"season_finale":     {FinaleSeason, true},
		"series_finale":     {FinaleSeries, true},
	}
	for raw, test := range tests {
		got := FinaleFromTrakt(raw)
		if got != test.want || got.CompletesSeason() != test.completes {
			t.Fatalf("FinaleFromTrakt(%q)=%q completes=%v", raw, got, got.CompletesSeason())
		}
	}
}
