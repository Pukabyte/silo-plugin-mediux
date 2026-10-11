package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// movieCollectionSets groups this movie's collection assets into the same
// candidates as standalone sets. Collection covers and other movies are ignored.
func movieCollectionSets(item map[string]json.RawMessage, movieID string) ([]set, error) {
	var sets []set
	indices := make(map[string]int)
	for _, scope := range []struct{ field, kind string }{{"posters", "poster"}, {"backdrops", "backdrop"}} {
		if len(item[scope.field]) == 0 {
			continue
		}
		var assets []struct {
			ID       string `json:"id"`
			Modified string `json:"modified_on"`
			Language struct {
				Name string `json:"display_name"`
			} `json:"language"`
			Movie *struct {
				ID string `json:"id"`
			} `json:"movie"`
			Set *struct {
				ID         string  `json:"id"`
				Popularity float64 `json:"popularity"`
				User       struct {
					Username string `json:"username"`
				} `json:"user_created"`
			} `json:"collection_set"`
		}
		if err := json.Unmarshal(item[scope.field], &assets); err != nil {
			return nil, fmt.Errorf("Mediux collection %s: %w", scope.field, err)
		}
		for _, asset := range assets {
			if asset.ID == "" || asset.Movie == nil || asset.Movie.ID != movieID || asset.Set == nil || asset.Set.ID == "" {
				continue
			}
			index, found := indices[asset.Set.ID]
			if !found {
				index = len(sets)
				indices[asset.Set.ID] = index
				sets = append(sets, set{ID: asset.Set.ID, Creator: asset.Set.User.Username, Popularity: asset.Set.Popularity})
			}
			modified, _ := time.Parse(time.RFC3339Nano, asset.Modified)
			sets[index].Art = append(sets[index].Art, artwork{
				ID: asset.ID, Creator: asset.Set.User.Username, Kind: scope.kind,
				SetID: asset.Set.ID, Popularity: asset.Set.Popularity,
				Modified: modified, Language: normalizeLanguage(asset.Language.Name),
			})
		}
	}
	return sets, nil
}
