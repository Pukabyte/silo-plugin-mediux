package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

// Mediux popularity_global is a rank among all sets: 1 is the best set, and
// 0 or null means the set has no rank and goes last. In these tests the best
// set always has the alphabetically last ID, so the final ID tie-break would
// pick the wrong set if the rank were ignored, and the ranked sets are listed
// worst first so that response order cannot make a test pass either.

func TestMediuxRankBefore(t *testing.T) {
	for _, tc := range []struct {
		a, b mediuxRank
		want bool
	}{
		{1, 2, true},
		{2, 1, false},
		{3, 3, false},
		{1, 0, true},
		{0, 1, false},
		{0, 0, false},
		{17107, 17108, true},
	} {
		if got := tc.a.before(tc.b); got != tc.want {
			t.Errorf("%v.before(%v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// requireGlobalPopularity checks that every set selection in the query asks for
// popularity_global and none asks for the per-title popularity: the movie query
// has three set selections (movie_sets and two collection_set), the show query one.
func requireGlobalPopularity(t *testing.T, r *http.Request, selections int) {
	t.Helper()
	var request struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		t.Error(err)
		return
	}
	if global, any := strings.Count(request.Query, "popularity_global"), strings.Count(request.Query, "popularity"); global != selections || any != selections {
		t.Errorf("query has %d popularity_global and %d popularity fields, want %d of each: %s", global, any, selections, request.Query)
	}
}

const rankedMovieResponse = `{
  "data": {
    "movies_by_id": {
      "id": "100",
      "movie_sets": [
        {"id": "set-a", "popularity_global": null, "user_created": {"username": "maker"},
         "movie_poster": [{"id": "poster-a-unranked", "modified_on": "2026-10-11T01:02:03Z", "language": {"display_name": "English"}}],
         "movie_backdrop": [], "files": []},
        {"id": "set-b", "popularity_global": 12324, "user_created": {"username": "maker"},
         "movie_poster": [{"id": "poster-b-second", "modified_on": "2026-10-11T01:02:03Z", "language": {"display_name": "English"}}],
         "movie_backdrop": [], "files": []},
        {"id": "set-c", "popularity_global": 10642, "user_created": {"username": "maker"},
         "movie_poster": [{"id": "poster-c-best", "modified_on": "2026-10-11T01:02:03Z", "language": {"display_name": "English"}}],
         "movie_backdrop": [], "files": []}
      ],
      "posters": [
        {"id": "poster-d-collection", "modified_on": "2026-10-11T01:02:03Z", "language": {"display_name": "English"}, "movie": {"id": "100"},
         "collection_set": {"id": "set-d", "popularity_global": 11000, "user_created": {"username": "maker"}}}
      ],
      "backdrops": []
    }
  }
}`

func TestMoviePosterPrefersBestGlobalMediuxRank(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireGlobalPopularity(t, r, 3)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rankedMovieResponse))
	}))
	defer upstream.Close()

	p := newProvider()
	p.endpoint = upstream.URL
	p.client = &http.Client{Timeout: time.Second}
	p.configure(map[string]any{"api_token": "safe", "cache_hours": 24, "image_selection": "BestOnly", "priority_creators": "maker"})
	s := &server{p: p}
	providerIDs, _ := structpb.NewStruct(map[string]any{"tmdb": "100"})
	req := &pluginv1.GetImagesRequest{ItemType: "movie", ProviderIds: providerIDs, Language: "en"}

	automatic, err := s.GetImages(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(imageIDs(automatic), ","); got != "poster-c-best" {
		t.Fatalf("automatic poster = %q, want poster-c-best", got)
	}

	// The collection set's global rank sits between the two movie sets, so it
	// is ordered on the same scale as them.
	galleryCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(imageRequestPurposeHeader, "gallery"))
	gallery, err := s.GetImages(galleryCtx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(imageIDs(gallery), ","); got != "poster-c-best,poster-d-collection,poster-b-second,poster-a-unranked" {
		t.Fatalf("gallery order = %q", got)
	}
}

func TestSelectArtOrdersMediuxRankAscendingWithUnrankedLast(t *testing.T) {
	p := newProvider()
	b := bundle{Sets: []set{
		{ID: "a", Creator: "maker", GlobalRank: 0, Art: []artwork{{ID: "a-unranked", Kind: "poster", SetID: "a"}}},
		{ID: "b", Creator: "maker", GlobalRank: 30, Art: []artwork{{ID: "b-third", Kind: "poster", SetID: "b"}}},
		{ID: "d", Creator: "maker", GlobalRank: 10, Art: []artwork{{ID: "d-first", Kind: "poster", SetID: "d"}}},
		{ID: "c", Creator: "maker", GlobalRank: 20, Art: []artwork{{ID: "c-second", Kind: "poster", SetID: "c"}}},
	}}
	got := p.selectArt(b, p.snapshot(), "poster", 0, 0, true)
	ids := make([]string, 0, len(got))
	for _, a := range got {
		ids = append(ids, a.ID)
	}
	if joined := strings.Join(ids, ","); joined != "d-first,c-second,b-third,a-unranked" {
		t.Fatalf("order = %q", joined)
	}
}

func TestSelectArtCreatorPriorityStillPrecedesMediuxRank(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{"priority_creators": "first,second"})
	b := bundle{Sets: []set{
		{ID: "a", Creator: "second", GlobalRank: 1, Art: []artwork{{ID: "a-second-creator-best-rank", Kind: "poster", SetID: "a"}}},
		{ID: "b", Creator: "first", GlobalRank: 500, Art: []artwork{{ID: "b-first-creator", Kind: "poster", SetID: "b"}}},
	}}
	got := p.selectArt(b, p.snapshot(), "poster", 0, 0, false)
	if len(got) != 1 || got[0].ID != "b-first-creator" {
		t.Fatalf("best = %#v", got)
	}
}

func TestConsistentSetPrefersBestMediuxRank(t *testing.T) {
	p := newProvider()
	for _, mode := range []string{"MostComplete", "CreatorPriority"} {
		cfg := settings{SeasonConsistency: mode, TitlecardConsistency: mode}
		for _, tc := range []struct {
			name string
			sets []set
			want string
		}{
			{"better rank wins", []set{
				{ID: "a-second", Creator: "maker", GlobalRank: 200, Art: []artwork{consistencyArt(1, "season", "a-second", 1, 0, "en"), consistencyArt(2, "still", "a-second", 1, 1, "en")}},
				{ID: "b-best", Creator: "maker", GlobalRank: 100, Art: []artwork{consistencyArt(3, "season", "b-best", 1, 0, "en"), consistencyArt(4, "still", "b-best", 1, 1, "en")}},
			}, "b-best"},
			{"ranked beats unranked", []set{
				{ID: "a-unranked", Creator: "maker", GlobalRank: 0, Art: []artwork{consistencyArt(5, "season", "a-unranked", 1, 0, "en"), consistencyArt(6, "still", "a-unranked", 1, 1, "en")}},
				{ID: "b-ranked", Creator: "maker", GlobalRank: 700, Art: []artwork{consistencyArt(7, "season", "b-ranked", 1, 0, "en"), consistencyArt(8, "still", "b-ranked", 1, 1, "en")}},
			}, "b-ranked"},
			{"equal rank falls back to set ID", []set{
				{ID: "b-same", Creator: "maker", GlobalRank: 50, Art: []artwork{consistencyArt(9, "season", "b-same", 1, 0, "en"), consistencyArt(10, "still", "b-same", 1, 1, "en")}},
				{ID: "a-same", Creator: "maker", GlobalRank: 50, Art: []artwork{consistencyArt(11, "season", "a-same", 1, 0, "en"), consistencyArt(12, "still", "a-same", 1, 1, "en")}},
			}, "a-same"},
		} {
			b := bundle{Sets: tc.sets}
			for _, kind := range []string{"season", "still"} {
				if got := p.consistentSet(b, cfg, kind, "en"); got != tc.want {
					t.Errorf("%s %s %s: got %q, want %q", mode, kind, tc.name, got, tc.want)
				}
			}
		}
	}
}

func TestSeriesArtworkPrefersBestGlobalMediuxRank(t *testing.T) {
	titlecard := func(n int) string {
		return `"titlecards":[{"id":"` + consistencyID(n) + `","modified_on":"2026-10-11T01:02:03Z","episode":{"episode_number":1,"season_id":{"season_number":1}}}]`
	}
	showSet := func(id string, rank string, n int) string {
		return `{"id":"` + id + `","popularity_global":` + rank + `,"user_created":{"username":"maker"},"show_poster":[{"id":"` + consistencyID(n) + `","modified_on":"2026-10-11T01:02:03Z","language":{"display_name":"English"}}],"show_backdrop":[],` +
			`"season_posters":[{"id":"` + consistencyID(n+1) + `","modified_on":"2026-10-11T01:02:03Z","language":{"display_name":"English"},"season":{"season_number":1}}],` + titlecard(n+2) + `,"files":[]}`
	}
	// The best set has the highest asset IDs, so ID order alone would pick the other set.
	response := `{"data":{"shows_by_id":{"id":"200","show_sets":[` + showSet("a-second", "900", 11) + `,` + showSet("b-best", "400", 21) + `]}}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requireGlobalPopularity(t, r, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	defer upstream.Close()

	for _, mode := range []string{"MostComplete", "CreatorPriority", "SeriesPoster"} {
		p := newProvider()
		p.endpoint = upstream.URL
		p.client = &http.Client{Timeout: time.Second}
		p.configure(map[string]any{"api_token": "safe", "cache_hours": 24, "image_selection": "BestOnly", "season_consistency": mode, "titlecard_consistency": mode})
		s := &server{p: p}
		providerIDs, _ := structpb.NewStruct(map[string]any{"tmdb": "200"})

		poster, err := s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "series", ProviderIds: providerIDs, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(imageIDs(poster), ","); got != consistencyID(21) {
			t.Fatalf("%s series poster = %q, want %s", mode, got, consistencyID(21))
		}
		season := int32(1)
		seasonImages, err := s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "series", ProviderIds: providerIDs, SeasonNumber: &season, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(imageIDs(seasonImages), ","); got != consistencyID(22) {
			t.Fatalf("%s season poster = %q, want %s", mode, got, consistencyID(22))
		}
		episodes, err := s.GetEpisodes(context.Background(), &pluginv1.GetEpisodesRequest{ProviderIds: providerIDs, SeasonNumber: 1, Language: "en"})
		if err != nil {
			t.Fatal(err)
		}
		if len(episodes.GetEpisodes()) != 1 || !strings.Contains(episodes.GetEpisodes()[0].GetStillPath(), consistencyID(23)) {
			t.Fatalf("%s title card = %#v, want %s", mode, episodes.GetEpisodes(), consistencyID(23))
		}
	}
}
