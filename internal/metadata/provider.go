// Package metadata defines provider-neutral television metadata used by prompt rules.
package metadata

import "context"

type FinaleType string

const (
	FinaleNone      FinaleType = "none"
	FinaleMidSeason FinaleType = "mid_season"
	FinaleSeason    FinaleType = "season"
	FinaleSeries    FinaleType = "series"
)

func (f FinaleType) CompletesSeason() bool {
	return f == FinaleSeason || f == FinaleSeries
}

type EpisodeRef struct {
	ShowIDs       map[string]string
	SeasonNumber  int
	EpisodeNumber int
}

type Episode struct {
	IDs      map[string]string
	Number   int
	Released bool
	Finale   FinaleType
}

type Season struct {
	ShowIDs  map[string]string
	Number   int
	Episodes []Episode
}

// Provider supplies normalized metadata. Implementations may use Trakt, Sonarr,
// Jellyfin, TheTVDB, or another configured source without changing prompt rules.
type Provider interface {
	Name() string
	Season(context.Context, EpisodeRef) (Season, error)
}

func FinaleFromTrakt(value string) FinaleType {
	switch value {
	case "mid_season_finale":
		return FinaleMidSeason
	case "season_finale":
		return FinaleSeason
	case "series_finale":
		return FinaleSeries
	default:
		return FinaleNone
	}
}
