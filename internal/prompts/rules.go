package prompts

import "sort"

type Kind string

const (
	MovieRating   Kind = "movie_rating"
	SeasonRating  Kind = "season_rating"
	EpisodeRating Kind = "episode_rating"
)

type Episode struct {
	ID                int64
	Number            int
	Released          bool
	Watched           bool
	PreviouslyWatched bool
	Normal            bool
}

type SeasonState struct {
	SeasonID       int64
	ShowID         int64
	Episodes       []Episode
	InventoryKnown bool
}

type Batch struct {
	Baseline        bool
	NewMovieWatches []int64
	NewEpisodeIDs   []int64
	Seasons         []SeasonState
	IgnoredMovieIDs map[int64]bool
	IgnoredShowIDs  map[int64]bool
}

type Decision struct {
	Kind    Kind
	MediaID int64
}

func Evaluate(batch Batch) []Decision {
	if batch.Baseline {
		return nil
	}

	decisions := make([]Decision, 0, len(batch.NewMovieWatches)+len(batch.Seasons))
	for _, movieID := range batch.NewMovieWatches {
		if movieID != 0 && !batch.IgnoredMovieIDs[movieID] {
			decisions = append(decisions, Decision{Kind: MovieRating, MediaID: movieID})
		}
	}

	newEpisodes := make(map[int64]bool, len(batch.NewEpisodeIDs))
	for _, id := range batch.NewEpisodeIDs {
		newEpisodes[id] = true
	}

	for _, season := range batch.Seasons {
		if !season.InventoryKnown || batch.IgnoredShowIDs[season.ShowID] {
			continue
		}

		normal := normalEpisodes(season.Episodes)
		if len(normal) == 0 {
			continue
		}

		allReleased := true
		allReleasedWatched := true
		newFirstWatch := false
		var newestReleased *Episode
		futureExpected := false
		for i := range normal {
			episode := &normal[i]
			if newEpisodes[episode.ID] && episode.Watched && !episode.PreviouslyWatched {
				newFirstWatch = true
			}
			if !episode.Released {
				allReleased = false
				futureExpected = true
				continue
			}
			if !episode.Watched {
				allReleasedWatched = false
			}
			if newestReleased == nil || episode.Number > newestReleased.Number {
				newestReleased = episode
			}
		}

		if allReleased && allReleasedWatched {
			if newFirstWatch {
				decisions = append(decisions, Decision{Kind: SeasonRating, MediaID: season.SeasonID})
			}
			continue
		}

		if !allReleasedWatched || !futureExpected || newestReleased == nil || !newestReleased.Watched {
			continue
		}
		if newEpisodes[newestReleased.ID] && !newestReleased.PreviouslyWatched {
			decisions = append(decisions, Decision{Kind: EpisodeRating, MediaID: newestReleased.ID})
		}
	}

	sort.SliceStable(decisions, func(i, j int) bool {
		if decisions[i].Kind == decisions[j].Kind {
			return decisions[i].MediaID < decisions[j].MediaID
		}
		return decisions[i].Kind < decisions[j].Kind
	})
	return decisions
}

func normalEpisodes(episodes []Episode) []Episode {
	normal := make([]Episode, 0, len(episodes))
	for _, episode := range episodes {
		if episode.Normal {
			normal = append(normal, episode)
		}
	}
	return normal
}
