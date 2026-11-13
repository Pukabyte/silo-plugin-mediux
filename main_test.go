package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestManifestLoads(t *testing.T) {
	m, err := manifest.Load(manifestJSON)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.GetCapabilities()[0].GetMetadata().GetFields()["lookup_provider_ids"].GetListValue().GetValues(); len(got) != 2 || got[0].GetStringValue() != "tmdb" || got[1].GetStringValue() != "tvdb" {
		t.Fatal("metadata lookup provider IDs missing")
	}
}

func TestResolverNeverLeaksToken(t *testing.T) {
	p := "mediux://295df690-c7c9-42f5-8496-0d2088c521f1?v=20261009120000&quality=original"
	got := resolve(p, "card")
	if got != "https://images.mediux.io/assets/295df690-c7c9-42f5-8496-0d2088c521f1?v=20261009120000" || strings.Contains(got, "token") {
		t.Fatalf("resolve = %q", got)
	}
	if resolve("https://evil.example/x", "") != "" {
		t.Fatal("accepted non-mediux path")
	}
	if resolve("mediux://not-an-asset?v=x&quality=original", "") != "" {
		t.Fatal("accepted invalid asset")
	}
}

func TestRankingExclusionAndGapFill(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{"priority_creators": "fav", "excluded_creators": "bad", "image_selection": "BestOnly"})
	b := bundle{Sets: []set{
		{Creator: "bad", GlobalRank: 1, Art: []artwork{{ID: "bad", Kind: "poster"}}},
		{Creator: "other", GlobalRank: 2, Art: []artwork{{ID: "other", Kind: "poster"}}},
		{Creator: "fav", GlobalRank: 50, Art: []artwork{{ID: "fav", Kind: "poster"}}},
	}}
	got := p.selectArt(b, p.snapshot(), "poster", 0, 0, false)
	if len(got) != 1 || got[0].ID != "fav" {
		t.Fatalf("best = %#v", got)
	}
	p.configure(map[string]any{"priority_creators": "missing", "only_priority_creators": false})
	got = p.selectArt(b, p.snapshot(), "poster", 0, 0, false)
	if len(got) != 1 || got[0].ID != "other" {
		t.Fatalf("gap-fill = %#v", got)
	}
	b = bundle{Sets: []set{{Creator: "z", GlobalRank: 1, Art: []artwork{{ID: "high", Creator: "z", Kind: "poster"}}}, {Creator: "a", GlobalRank: 100, Art: []artwork{{ID: "low", Creator: "a", Kind: "poster"}}}}}
	got = p.selectArt(b, p.snapshot(), "poster", 0, 0, false)
	if len(got) != 1 || got[0].ID != "high" || got[0].Creator != "z" {
		t.Fatalf("unpreferred rank=%#v", got)
	}
}

func TestSeriesFetchCachedAndGraphQLErrorsFail(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer safe" {
			t.Error("missing bearer")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"shows_by_id":{"show_sets":[{"id":"x","popularity_global":3,"user_created":{"username":"one"},"show_poster":[{"id":"poster","modified_on":"2023-12-14T12:45:51.000Z"}],"show_backdrop":[],"season_posters":[{"id":"s0","modified_on":"2023-12-14T12:45:30.000Z","season":{"season_number":0}}],"titlecards":[{"id":"e","modified_on":"2023-12-16T12:54:02.000Z","episode":{"episode_number":2,"season_id":{"season_number":0}}}],"files":[]}]}}}`))
	}))
	defer srv.Close()
	p := newProvider()
	p.endpoint = srv.URL
	p.client = &http.Client{Timeout: time.Second}
	p.configure(map[string]any{"api_token": "safe", "cache_hours": 24})
	if _, err := p.get(context.Background(), "series", "12"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.get(context.Background(), "series", "12"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"errors":[{"message":"bad"}]}`)) }))
	defer bad.Close()
	p.endpoint = bad.URL
	p.configure(map[string]any{"api_token": "safe"})
	if _, err := p.get(context.Background(), "series", "13"); err == nil {
		t.Fatal("GraphQL errors accepted")
	}
}

func TestParseLiveShapeArraysAndModifiedVersion(t *testing.T) {
	raw := json.RawMessage(`{"shows_by_id":{"id":"1399","show_sets":[{"id":"493","popularity_global":19,"user_created":{"username":"Aloha_Alona"},"show_poster":[{"id":"295df690-c7c9-42f5-8496-0d2088c521f1","modified_on":"2023-12-14T12:45:51.000Z","language":{"display_name":"English"}}],"show_backdrop":[{"id":"ec1c004a-ed29-4406-9d69-c8cfe277e0cc","modified_on":"2023-12-14T12:51:22.000Z"}],"season_posters":[{"id":"1c3ba710-ec6e-4aaf-8c4c-57923070d2e9","modified_on":"2023-12-14T12:45:30.000Z","season":{"season_number":1}}],"titlecards":[{"id":"01dd338d-230c-44de-82db-770c69b97609","modified_on":"2023-12-16T12:54:02.000Z","episode":{"episode_number":6,"season_id":{"season_number":4}}}],"files":[]}]}}`)
	b, err := parseBundle(raw, "series", "1399")
	if err != nil || len(b.Sets) != 1 || len(b.Sets[0].Art) != 4 {
		t.Fatalf("parse: %#v %v", b, err)
	}
	if got := canonical(b.Sets[0].Art[0], "optimized"); !strings.Contains(got, "v=20231214124551") {
		t.Fatalf("version = %s", got)
	}
	if b.Sets[0].Art[0].Language != "en" {
		t.Fatalf("language=%q", b.Sets[0].Art[0].Language)
	}
	if b.Sets[0].Art[0].SetID != "493" {
		t.Fatalf("set ID=%q", b.Sets[0].Art[0].SetID)
	}
}

func TestNormalizeLanguage(t *testing.T) {
	for input, want := range map[string]string{"English": "en", "Brazilian Portuguese": "pt-br", "pt-BR": "pt-br", "zh-CN": "zh-cn", "zh-tw": "zh-tw", "unknown": ""} {
		if got := normalizeLanguage(input); got != want {
			t.Fatalf("%s=%s", input, got)
		}
	}
}

func TestBestOnlyUsesLanguageTierAfterCreatorRank(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{"api_token": "safe", "priority_creators": "", "cache_hours": 24, "image_selection": "BestOnly"})
	b := bundle{Sets: []set{{Creator: "high", GlobalRank: 1, Art: []artwork{{ID: "neutral", Creator: "high", Kind: "poster", GlobalRank: 1}}}, {Creator: "low", GlobalRank: 100, Art: []artwork{{ID: "english", Creator: "low", Kind: "poster", GlobalRank: 100, Language: "en"}}}, {Creator: "preferred", GlobalRank: 100, Art: []artwork{{ID: "english-preferred", Creator: "preferred", Kind: "poster", GlobalRank: 100, Language: "en"}}}}}
	p.configure(map[string]any{"api_token": "safe", "priority_creators": "preferred", "cache_hours": 24, "image_selection": "BestOnly"})
	candidates := p.selectArt(b, p.snapshot(), "poster", 0, 0, true)
	chosen := bestForLanguage(candidates, "poster", "en")
	if len(chosen) != 1 || chosen[0].ID != "english-preferred" {
		t.Fatalf("language tier=%#v", chosen)
	}
	// A non-preferred English candidate still beats a more-popular neutral one.
	p.configure(map[string]any{"api_token": "safe", "priority_creators": "", "cache_hours": 24, "image_selection": "BestOnly"})
	candidates = p.selectArt(b, p.snapshot(), "poster", 0, 0, true)
	chosen = bestForLanguage(candidates, "poster", "en")
	if len(chosen) != 1 || chosen[0].ID != "english" {
		t.Fatalf("tier=%#v", chosen)
	}
}

func TestAutomaticArtworkStaysInWinningSeriesSet(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{"season_consistency": "SeriesPoster", "titlecard_consistency": "SeriesPoster"})
	p.configure(map[string]any{"api_token": "safe", "cache_hours": 24, "image_selection": "BestOnly"})
	b := bundle{Sets: []set{
		{ID: "set-a", Creator: "same", GlobalRank: 91, Art: []artwork{
			{ID: "series-a", Kind: "poster", SetID: "set-a", Language: "en"},
			{ID: "backdrop-a", Kind: "backdrop", SetID: "set-a"},
			{ID: "logo-a", Kind: "logo", SetID: "set-a", Language: "en"},
			{ID: "season5-a", Kind: "season", SetID: "set-a", Season: 5, Language: "en"},
			{ID: "special-a", Kind: "season", SetID: "set-a", Season: 0, Language: "en"},
			{ID: "episode-a", Kind: "still", SetID: "set-a", Season: 5, Episode: 1},
		}},
		{ID: "set-b", Creator: "same", GlobalRank: 1, Art: []artwork{
			{ID: "series-b", Kind: "poster", SetID: "set-b", Language: "fr"},
			{ID: "backdrop-b", Kind: "backdrop", SetID: "set-b"},
			{ID: "logo-b", Kind: "logo", SetID: "set-b", Language: "es"},
			{ID: "season5-b", Kind: "season", SetID: "set-b", Season: 5, Language: "es"},
			{ID: "season6-b", Kind: "season", SetID: "set-b", Season: 6, Language: "es"},
			{ID: "special-b", Kind: "season", SetID: "set-b", Season: 0, Language: "es"},
			{ID: "episode-b", Kind: "still", SetID: "set-b", Season: 5, Episode: 1},
		}},
	}}
	if got := p.seriesSet(b, p.snapshot(), "es"); got != "set-a" {
		t.Fatalf("series anchor=%q", got)
	}
	for _, tc := range []struct {
		kind            string
		season, episode int
		want            string
	}{
		{"backdrop", 0, 0, "backdrop-a"},
		{"logo", 0, 0, "logo-a"},
		{"season", 5, 0, "season5-a"},
		{"season", 0, 0, "special-a"},
		{"still", 5, 1, "episode-a"},
		// Set A has no sixth-season poster, so normal global ranking fills it.
		{"season", 6, 0, "season6-b"},
	} {
		got := p.automaticArt(b, p.snapshot(), tc.kind, tc.season, tc.episode, "es")
		if len(got) != 1 || got[0].ID != tc.want {
			t.Fatalf("%s %d/%d = %#v, want %s", tc.kind, tc.season, tc.episode, got, tc.want)
		}
	}
}

func TestServiceUsesSetAnchorButAllGalleryRemainsComplete(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{"season_consistency": "SeriesPoster", "titlecard_consistency": "SeriesPoster"})
	p.configure(map[string]any{"api_token": "safe", "cache_hours": 24, "image_selection": "BestOnly"})
	b := bundle{Sets: []set{
		{ID: "a", GlobalRank: 100, Art: []artwork{{ID: "poster-a", Kind: "poster", SetID: "a", Language: "en"}, {ID: "season-a", Kind: "season", SetID: "a", Season: 5, Language: "en"}}},
		{ID: "b", GlobalRank: 1, Art: []artwork{{ID: "poster-b", Kind: "poster", SetID: "b", Language: "fr"}, {ID: "season-b", Kind: "season", SetID: "b", Season: 5, Language: "es"}}},
	}}
	cacheBundle := func() {
		p.mu.Lock()
		p.cache[fmt.Sprintf("%d:series:1", p.generation)] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
		p.mu.Unlock()
	}
	cacheBundle()
	s := &server{p: p}
	providerIDs, _ := structpb.NewStruct(map[string]any{"tmdb": "1"})
	season := int32(5)
	images, err := s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "series", ProviderIds: providerIDs, SeasonNumber: &season, Language: "es"})
	if err != nil || len(images.Images) != 1 || !strings.Contains(images.Images[0].Url, "season-a") {
		t.Fatalf("automatic season image: %#v %v", images, err)
	}
	seasons, err := s.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{ProviderIds: providerIDs, Language: "es"})
	if err != nil || len(seasons.Seasons) != 1 || !strings.Contains(seasons.Seasons[0].PosterPath, "season-a") {
		t.Fatalf("automatic season record: %#v %v", seasons, err)
	}
	p.configure(map[string]any{"api_token": "safe", "cache_hours": 24, "image_selection": "All"})
	cacheBundle()
	images, err = s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "series", ProviderIds: providerIDs, SeasonNumber: &season, Language: "es"})
	if err != nil || len(images.Images) != 2 {
		t.Fatalf("all gallery: %#v %v", images, err)
	}
	seasons, err = s.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{ProviderIds: providerIDs, Language: "es"})
	if err != nil || len(seasons.Seasons) != 1 || !strings.Contains(seasons.Seasons[0].PosterPath, "season-a") {
		t.Fatalf("all season record: %#v %v", seasons, err)
	}
}

func TestGetImages_GalleryPurposeReturnsAllEligibleArtwork(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{
		"api_token":              "safe",
		"image_selection":        "BestOnly",
		"priority_creators":      "fav",
		"excluded_creators":      "blocked",
		"only_priority_creators": true,
	})
	b := bundle{Sets: []set{
		{ID: "fav", Creator: "fav", GlobalRank: 100, Art: []artwork{
			{ID: "poster-fav-1", Kind: "poster", SetID: "fav"},
			{ID: "poster-fav-2", Kind: "poster", SetID: "fav"},
			{ID: "backdrop-fav-1", Kind: "backdrop", SetID: "fav"},
			{ID: "backdrop-fav-2", Kind: "backdrop", SetID: "fav"},
			{ID: "logo-fav-1", Kind: "logo", SetID: "fav"},
			{ID: "logo-fav-2", Kind: "logo", SetID: "fav"},
			{ID: "season1-fav-1", Kind: "season", SetID: "fav", Season: 1},
			{ID: "season1-fav-2", Kind: "season", SetID: "fav", Season: 1},
			{ID: "special-fav-1", Kind: "season", SetID: "fav", Season: 0},
			{ID: "special-fav-2", Kind: "season", SetID: "fav", Season: 0},
		}},
		{ID: "blocked", Creator: "blocked", GlobalRank: 1, Art: []artwork{
			{ID: "poster-blocked", Kind: "poster", SetID: "blocked"},
			{ID: "season1-blocked", Kind: "season", SetID: "blocked", Season: 1},
			{ID: "special-blocked", Kind: "season", SetID: "blocked", Season: 0},
		}},
		{ID: "other", Creator: "other", GlobalRank: 2, Art: []artwork{
			{ID: "poster-other", Kind: "poster", SetID: "other"},
			{ID: "season1-other", Kind: "season", SetID: "other", Season: 1},
			{ID: "special-other", Kind: "season", SetID: "other", Season: 0},
		}},
	}}
	p.cache[fmt.Sprintf("%d:movie:1", p.generation)] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	p.cache[fmt.Sprintf("%d:series:2", p.generation)] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	s := &server{p: p}
	movieIDs, _ := structpb.NewStruct(map[string]any{"tmdb": "1"})
	seriesIDs, _ := structpb.NewStruct(map[string]any{"tmdb": "2"})
	season1, specials := int32(1), int32(0)

	tests := []struct {
		name string
		req  *pluginv1.GetImagesRequest
		want []string
	}{
		{"movie", &pluginv1.GetImagesRequest{ItemType: "movie", ProviderIds: movieIDs}, []string{"poster-fav-1", "poster-fav-2", "backdrop-fav-1", "backdrop-fav-2", "logo-fav-1", "logo-fav-2"}},
		{"series", &pluginv1.GetImagesRequest{ItemType: "series", ProviderIds: seriesIDs}, []string{"poster-fav-1", "poster-fav-2", "backdrop-fav-1", "backdrop-fav-2", "logo-fav-1", "logo-fav-2"}},
		{"season", &pluginv1.GetImagesRequest{ItemType: "series", ProviderIds: seriesIDs, SeasonNumber: &season1}, []string{"season1-fav-1", "season1-fav-2"}},
		{"specials", &pluginv1.GetImagesRequest{ItemType: "series", ProviderIds: seriesIDs, SeasonNumber: &specials}, []string{"special-fav-1", "special-fav-2"}},
	}
	galleryCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(imageRequestPurposeHeader, "gallery"))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			images, err := s.GetImages(galleryCtx, tc.req)
			if err != nil {
				t.Fatal(err)
			}
			got := imageIDs(images)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("gallery images = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetImages_GalleryPurposeDoesNotChangeAutomaticSelection(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{"api_token": "safe", "image_selection": "BestOnly"})
	b := bundle{Sets: []set{
		{ID: "preferred", Creator: "preferred", GlobalRank: 100, Art: []artwork{{ID: "preferred", Kind: "poster", SetID: "preferred"}}},
		{ID: "popular", Creator: "popular", GlobalRank: 1, Art: []artwork{{ID: "popular", Kind: "poster", SetID: "popular"}}},
	}}
	p.cache[fmt.Sprintf("%d:movie:1", p.generation)] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	s := &server{p: p}
	ids, _ := structpb.NewStruct(map[string]any{"tmdb": "1"})
	req := &pluginv1.GetImagesRequest{ItemType: "movie", ProviderIds: ids}

	for _, ctx := range []context.Context{
		context.Background(),
		metadata.NewIncomingContext(context.Background(), metadata.Pairs(imageRequestPurposeHeader, "automatic")),
		metadata.NewIncomingContext(context.Background(), metadata.Pairs(imageRequestPurposeHeader, "gallery", imageRequestPurposeHeader, "gallery")),
		metadata.NewIncomingContext(context.Background(), metadata.Pairs(imageRequestPurposeHeader, "unknown")),
	} {
		images, err := s.GetImages(ctx, req)
		if err != nil || strings.Join(imageIDs(images), ",") != "popular" {
			t.Fatalf("automatic images = %v, err = %v", imageIDs(images), err)
		}
	}
}

func imageIDs(response *pluginv1.GetImagesResponse) []string {
	ids := make([]string, 0, len(response.GetImages()))
	for _, image := range response.GetImages() {
		path := strings.TrimPrefix(image.GetUrl(), "mediux://")
		ids = append(ids, strings.SplitN(path, "?", 2)[0])
	}
	return ids
}

func TestEpisodesUseLanguageMatchedSeriesSet(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{"season_consistency": "SeriesPoster", "titlecard_consistency": "SeriesPoster"})
	p.configure(map[string]any{"api_token": "safe", "cache_hours": 24, "image_selection": "BestOnly"})
	b := bundle{Sets: []set{
		{ID: "english", GlobalRank: 1, Art: []artwork{
			{ID: "poster-en", Kind: "poster", SetID: "english", Language: "en"},
			{ID: "still-en", Kind: "still", SetID: "english", Season: 1, Episode: 1, Language: "en"},
		}},
		{ID: "german", GlobalRank: 100, Art: []artwork{
			{ID: "poster-de", Kind: "poster", SetID: "german", Language: "de"},
			{ID: "still-de", Kind: "still", SetID: "german", Season: 1, Episode: 1, Language: "de"},
		}},
	}}
	p.mu.Lock()
	p.cache[fmt.Sprintf("%d:series:2", p.generation)] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	p.mu.Unlock()
	s := &server{p: p}
	providerIDs, _ := structpb.NewStruct(map[string]any{"tmdb": "2"})
	episodes, err := s.GetEpisodes(context.Background(), &pluginv1.GetEpisodesRequest{ProviderIds: providerIDs, SeasonNumber: 1, Language: "de"})
	if err != nil || len(episodes.Episodes) != 1 || !strings.Contains(episodes.Episodes[0].StillPath, "still-de") {
		t.Fatalf("German titlecard did not use German poster set: %#v %v", episodes, err)
	}
}

func TestTitlecardLanguageWithinSelectedSetAndCreatorPriority(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{"priority_creators": "fav,other"})
	b := bundle{Sets: []set{
		{ID: "selected", Creator: "fav", Art: []artwork{
			{ID: "a-de", Kind: "still", SetID: "selected", Season: 1, Episode: 1, Language: "de"},
			{ID: "z-en", Kind: "still", SetID: "selected", Season: 1, Episode: 1, Language: "en"},
			{ID: "n-neutral", Kind: "still", SetID: "selected", Season: 1, Episode: 1},
		}},
		{ID: "fallback", Creator: "other", GlobalRank: 1, Art: []artwork{
			{ID: "0-en", Kind: "still", SetID: "fallback", Season: 1, Episode: 1, Language: "en"},
		}},
	}}
	for _, tc := range []struct{ language, want string }{{"en", "z-en"}, {"de", "a-de"}, {"fr", "z-en"}} {
		got := p.automaticArtWithSet(b, p.snapshot(), "still", 1, 1, tc.language, setSelection{ID: "selected"})
		if len(got) != 1 || got[0].ID != tc.want {
			t.Fatalf("language %s: got %#v, want %s", tc.language, got, tc.want)
		}
	}
	// Neutral cards precede a different language when no requested/English card exists.
	b.Sets[0].Art = append(b.Sets[0].Art[:1], b.Sets[0].Art[2])
	got := p.automaticArtWithSet(b, p.snapshot(), "still", 1, 1, "fr", setSelection{ID: "selected"})
	if len(got) != 1 || got[0].ID != "n-neutral" {
		t.Fatalf("neutral titlecard fallback: %#v", got)
	}
	// Missing-set fallback and Independent mode must preserve creator order.
	b.Sets[0].Art = b.Sets[0].Art[:1]
	for _, selection := range []setSelection{{}, {ID: "missing"}} {
		got = p.automaticArtWithSet(b, p.snapshot(), "still", 1, 1, "en", selection)
		if len(got) != 1 || got[0].ID != "a-de" {
			t.Fatalf("language changed creator priority: %#v", got)
		}
	}
}

func TestSpecialsAndEpisodesStayInSeason(t *testing.T) {
	p := newProvider()
	b := bundle{Sets: []set{{Creator: "x", Art: []artwork{{ID: "special", Kind: "season", Season: 0}, {ID: "zero", Kind: "still", Season: 0, Episode: 2}, {ID: "one", Kind: "still", Season: 1, Episode: 2}}}}}
	p.cache["0:series:1"] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	s := &server{p: p}
	ids, _ := structpb.NewStruct(map[string]any{"tmdb": "1"})
	season0 := int32(0)
	images, err := s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: "series", ProviderIds: ids, SeasonNumber: &season0})
	if err != nil || len(images.Images) != 1 || !strings.Contains(images.Images[0].Url, "special") {
		t.Fatalf("specials: %#v %v", images, err)
	}
	eps, err := s.GetEpisodes(context.Background(), &pluginv1.GetEpisodesRequest{ProviderIds: ids, SeasonNumber: 0})
	if err != nil || len(eps.Episodes) != 1 || !strings.Contains(eps.Episodes[0].StillPath, "zero") {
		t.Fatalf("episodes: %#v %v", eps, err)
	}
}

func TestConfigureInvalidatesCache(t *testing.T) {
	p := newProvider()
	p.cache["series:1"] = cacheEntry{until: time.Now().Add(time.Hour)}
	p.configure(map[string]any{"api_token": "safe", "quality": "original"})
	if len(p.cache) != 0 || p.snapshot().Quality != "original" {
		t.Fatal("reload did not replace cache/settings")
	}
}

func TestConfigureRequiresToken(t *testing.T) {
	s := &server{p: newProvider()}
	value, _ := structpb.NewStruct(map[string]any{"quality": "optimized"})
	if _, err := s.Configure(context.Background(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: value}}}); err == nil {
		t.Fatal("missing token accepted")
	}
	value, _ = structpb.NewStruct(map[string]any{"api_token": " "})
	if _, err := s.Configure(context.Background(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: value}}}); err == nil {
		t.Fatal("blank token accepted")
	}
}

func TestIdentityOwnIDAndRejectsInvalid(t *testing.T) {
	s := &server{p: newProvider()}
	own, _ := structpb.NewStruct(map[string]any{"mediux": "series:1399"})
	_, id, err := s.identity(context.Background(), "series", own, "")
	if err != nil || id != "1399" {
		t.Fatalf("own ID %q %v", id, err)
	}
	bad, _ := structpb.NewStruct(map[string]any{"tmdb": "wrong"})
	_, id, err = s.identity(context.Background(), "series", bad, "")
	if err != nil || id != "" {
		t.Fatalf("invalid id accepted %q", id)
	}
}

func TestCacheBoundAndNegativeTTL(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var q struct {
			Variables map[string]string `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		id := q.Variables["tmdb_id"]
		if id == "err" {
			_, _ = w.Write([]byte(`{"errors":[{"message":"no"}]}`))
			return
		}
		if id == "empty" {
			_, _ = w.Write([]byte(`{"data":{"shows_by_id":{"id":"empty","show_sets":[]}}}`))
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"data":{"shows_by_id":{"id":%q,"show_sets":[]}}}`, id)))
	}))
	defer srv.Close()
	p := newProvider()
	p.endpoint = srv.URL
	p.client = &http.Client{Timeout: time.Second}
	p.configure(map[string]any{"api_token": "safe", "cache_hours": 24})
	for i := 0; i < 129; i++ {
		if _, err := p.get(context.Background(), "series", fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(p.cache) > 128 {
		t.Fatalf("cache entries=%d", len(p.cache))
	}
	before := calls.Load()
	if _, err := p.get(context.Background(), "series", "empty"); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	entry := p.cache[fmt.Sprintf("%d:series:empty", p.generation)]
	p.mu.Unlock()
	if until := time.Until(entry.until); until <= 0 || until > 5*time.Minute+time.Second {
		t.Fatalf("negative TTL=%s", until)
	}
	if _, err := p.get(context.Background(), "series", "empty"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != before+1 {
		t.Fatalf("negative cache missed calls=%d", calls.Load()-before)
	}
	if _, err := p.get(context.Background(), "series", "err"); err == nil {
		t.Fatal("error accepted")
	}
	if _, err := p.get(context.Background(), "series", "err"); err == nil {
		t.Fatal("error accepted")
	}
	if calls.Load() != before+3 {
		t.Fatal("error was cached")
	}
	p.configure(map[string]any{"api_token": "safe", "cache_hours": 0})
	n := calls.Load()
	_, _ = p.get(context.Background(), "series", "zero")
	_, _ = p.get(context.Background(), "series", "zero")
	if calls.Load() != n+2 {
		t.Fatal("cache_hours=0 cached")
	}
}

func TestReloadDoesNotJoinOldFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			close(started)
			<-release
		}
		var q struct {
			Variables map[string]string `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"data":{"shows_by_id":{"id":%q,"show_sets":[]}}}`, q.Variables["tmdb_id"])))
	}))
	defer srv.Close()
	p := newProvider()
	p.endpoint = srv.URL
	p.client = &http.Client{Timeout: time.Second}
	p.configure(map[string]any{"api_token": "old", "cache_hours": 24})
	done := make(chan error, 1)
	go func() { _, err := p.get(context.Background(), "series", "1"); done <- err }()
	<-started
	p.configure(map[string]any{"api_token": "new", "cache_hours": 24})
	if _, err := p.get(context.Background(), "series", "1"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("reload joined old flight: %d", calls.Load())
	}
	p.mu.Lock()
	if len(p.cache) != 1 {
		t.Fatalf("new generation cache=%d", len(p.cache))
	}
	p.mu.Unlock()
}
