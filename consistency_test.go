package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func consistencyID(n int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
}

func consistencyArt(n int, kind, setID string, season, episode int, language string) artwork {
	return artwork{ID: consistencyID(n), Kind: kind, SetID: setID, Season: season, Episode: episode, Language: language}
}

func TestMostCompleteConsistencyIsIndependentByArtworkKind(t *testing.T) {
	p := newProvider()
	cfg := settings{SeasonConsistency: "MostComplete", TitlecardConsistency: "MostComplete"}
	b := bundle{Sets: []set{
		{ID: "seasons", Creator: "one", GlobalRank: 100, Art: []artwork{
			consistencyArt(1, "season", "seasons", 0, 0, "en"),
			consistencyArt(2, "season", "seasons", 1, 0, "en"),
			consistencyArt(3, "season", "seasons", 2, 0, "en"),
			consistencyArt(4, "still", "seasons", 1, 1, "en"),
		}},
		{ID: "cards", Creator: "two", GlobalRank: 1, Art: []artwork{
			consistencyArt(5, "season", "cards", 1, 0, "en"),
			consistencyArt(6, "still", "cards", 1, 1, "en"),
			consistencyArt(7, "still", "cards", 1, 2, "en"),
			consistencyArt(8, "still", "cards", 2, 1, "en"),
		}},
	}}
	if got := p.consistentSet(b, cfg, "season", "en"); got != "seasons" {
		t.Fatalf("season set=%q, want seasons", got)
	}
	if got := p.consistentSet(b, cfg, "still", "en"); got != "cards" {
		t.Fatalf("title-card set=%q, want cards", got)
	}
}

func TestMostCompleteCountsOnlyUniqueValidScopes(t *testing.T) {
	p := newProvider()
	cfg := settings{SeasonConsistency: "MostComplete", TitlecardConsistency: "MostComplete"}
	b := bundle{Sets: []set{
		{ID: "duplicates", Creator: "one", GlobalRank: 1, Art: []artwork{
			consistencyArt(10, "season", "duplicates", 1, 0, "en"),
			consistencyArt(11, "season", "duplicates", 1, 0, "en"),
			{ID: "", Kind: "season", SetID: "duplicates", Season: 2, Language: "en"},
			consistencyArt(12, "season", "duplicates", -1, 0, "en"),
			consistencyArt(13, "still", "duplicates", 1, 1, "en"),
			consistencyArt(14, "still", "duplicates", 1, 1, "en"),
			consistencyArt(15, "still", "duplicates", 1, 0, "en"),
		}},
		{ID: "complete", Creator: "two", GlobalRank: 100, Art: []artwork{
			consistencyArt(16, "season", "complete", 1, 0, "en"),
			consistencyArt(17, "season", "complete", 2, 0, "en"),
			consistencyArt(18, "still", "complete", 1, 1, "en"),
			consistencyArt(19, "still", "complete", 1, 2, "en"),
		}},
	}}
	if got := p.consistentSet(b, cfg, "season", "en"); got != "complete" {
		t.Fatalf("unique season coverage selected %q", got)
	}
	if got := p.consistentSet(b, cfg, "still", "en"); got != "complete" {
		t.Fatalf("unique title-card coverage selected %q", got)
	}
}

func TestMostCompleteCreatorRulesPrecedeCoverage(t *testing.T) {
	p := newProvider()
	cfg := settings{Priority: "preferred", Excluded: "blocked", SeasonConsistency: "MostComplete", TitlecardConsistency: "MostComplete"}
	b := bundle{Sets: []set{
		{ID: "blocked", Creator: "blocked", Art: []artwork{
			consistencyArt(20, "season", "blocked", 1, 0, "en"), consistencyArt(21, "season", "blocked", 2, 0, "en"), consistencyArt(22, "season", "blocked", 3, 0, "en"),
			consistencyArt(23, "still", "blocked", 1, 1, "en"), consistencyArt(24, "still", "blocked", 1, 2, "en"), consistencyArt(25, "still", "blocked", 1, 3, "en"),
		}},
		{ID: "other", Creator: "other", Art: []artwork{
			consistencyArt(26, "season", "other", 1, 0, "en"), consistencyArt(27, "season", "other", 2, 0, "en"), consistencyArt(28, "season", "other", 3, 0, "en"),
			consistencyArt(29, "still", "other", 1, 1, "en"), consistencyArt(32, "still", "other", 1, 2, "en"), consistencyArt(33, "still", "other", 1, 3, "en"),
		}},
		{ID: "preferred", Creator: "preferred", Art: []artwork{
			consistencyArt(30, "season", "preferred", 1, 0, "en"), consistencyArt(31, "season", "preferred", 2, 0, "en"), consistencyArt(34, "season", "preferred", 3, 0, "en"),
			consistencyArt(35, "still", "preferred", 1, 1, "en"), consistencyArt(36, "still", "preferred", 1, 2, "en"), consistencyArt(37, "still", "preferred", 1, 3, "en"),
		}},
	}}
	for _, kind := range []string{"season", "still"} {
		if got := p.consistentSet(b, cfg, kind, "en"); got != "preferred" {
			t.Fatalf("%s set=%q, want preferred", kind, got)
		}
	}
}

func TestFullCoverageBeatsIncompletePriorityThenFallsBackWhenExcluded(t *testing.T) {
	p := newProvider()
	b := bundle{Sets: []set{
		{ID: "preferred", Creator: "preferred", Art: []artwork{
			consistencyArt(60, "season", "preferred", 1, 0, "en"),
		}},
		{ID: "full", Creator: "other", Art: []artwork{
			consistencyArt(61, "season", "full", 1, 0, "en"),
			consistencyArt(62, "season", "full", 2, 0, "en"),
		}},
		{ID: "fallback", Creator: "third", Art: []artwork{
			consistencyArt(63, "season", "fallback", 2, 0, "en"),
		}},
	}}
	cfg := settings{Priority: "preferred", SeasonConsistency: "MostComplete", MissingArtwork: "Fallback"}
	if got := p.consistentSet(b, cfg, "season", "en"); got != "full" {
		t.Fatalf("complete lower-priority set=%q, want full", got)
	}

	cfg.Excluded = "other"
	if got := p.consistentSet(b, cfg, "season", "en"); got != "preferred" {
		t.Fatalf("excluded complete set=%q, want preferred incomplete set", got)
	}
	got := p.automaticArt(b, cfg, "season", 2, 0, "en")
	if len(got) != 1 || got[0].SetID != "fallback" {
		t.Fatalf("missing preferred scope did not fall back: %#v", got)
	}
}

func TestCreatorPriorityConsistencyPrefersRankedCreatorThenFillsMissingScopes(t *testing.T) {
	p := newProvider()
	b := bundle{Sets: []set{
		{ID: "preferred", Creator: "preferred", GlobalRank: 100, Art: []artwork{
			consistencyArt(100, "season", "preferred", 1, 0, "en"),
			consistencyArt(101, "still", "preferred", 1, 1, "en"),
		}},
		{ID: "complete", Creator: "other", GlobalRank: 1, Art: []artwork{
			consistencyArt(102, "season", "complete", 1, 0, "de"),
			consistencyArt(103, "season", "complete", 2, 0, "de"),
			consistencyArt(104, "still", "complete", 1, 1, "de"),
			consistencyArt(105, "still", "complete", 1, 2, "de"),
		}},
		{ID: "fallback", Creator: "third", GlobalRank: 99, Art: []artwork{
			consistencyArt(106, "season", "fallback", 2, 0, "en"),
			consistencyArt(107, "still", "fallback", 1, 2, "en"),
		}},
	}}
	cfg := settings{
		Priority:             "preferred,third",
		SeasonConsistency:    "CreatorPriority",
		TitlecardConsistency: "CreatorPriority",
		MissingArtwork:       "Fallback",
	}
	for _, kind := range []string{"season", "still"} {
		if got := p.consistentSet(b, cfg, kind, "en"); got != "preferred" {
			t.Fatalf("%s selected %q, want incomplete preferred set", kind, got)
		}
	}
	for _, tc := range []struct {
		kind            string
		season, episode int
		want            string
	}{
		{"season", 1, 0, "preferred"},
		{"still", 1, 1, "preferred"},
		{"season", 2, 0, "fallback"},
		{"still", 1, 2, "fallback"},
	} {
		got := p.automaticArt(b, cfg, tc.kind, tc.season, tc.episode, "en")
		if len(got) != 1 || got[0].SetID != tc.want {
			t.Fatalf("%s %d/%d=%#v, want %q", tc.kind, tc.season, tc.episode, got, tc.want)
		}
	}
}

func TestCreatorPriorityHonorsExclusionsOnlyPriorityAndCoverageWithinCreator(t *testing.T) {
	p := newProvider()
	b := bundle{Sets: []set{
		{ID: "preferred-small", Creator: "preferred", Art: []artwork{
			consistencyArt(110, "season", "preferred-small", 1, 0, "en"),
			consistencyArt(111, "still", "preferred-small", 1, 1, "en"),
		}},
		{ID: "preferred-wide", Creator: "preferred", Art: []artwork{
			consistencyArt(112, "season", "preferred-wide", 1, 0, "en"),
			consistencyArt(113, "season", "preferred-wide", 2, 0, "en"),
			consistencyArt(114, "still", "preferred-wide", 1, 1, "en"),
			consistencyArt(115, "still", "preferred-wide", 1, 2, "en"),
		}},
		{ID: "unlisted-complete", Creator: "other", Art: []artwork{
			consistencyArt(116, "season", "unlisted-complete", 1, 0, "en"),
			consistencyArt(117, "season", "unlisted-complete", 2, 0, "en"),
			consistencyArt(118, "season", "unlisted-complete", 3, 0, "en"),
			consistencyArt(119, "still", "unlisted-complete", 1, 1, "en"),
			consistencyArt(120, "still", "unlisted-complete", 1, 2, "en"),
			consistencyArt(121, "still", "unlisted-complete", 1, 3, "en"),
		}},
	}}
	cfg := settings{Priority: "preferred", SeasonConsistency: "CreatorPriority", TitlecardConsistency: "CreatorPriority"}
	for _, kind := range []string{"season", "still"} {
		if got := p.consistentSet(b, cfg, kind, "en"); got != "preferred-wide" {
			t.Fatalf("%s selected %q, want wider set from same preferred creator", kind, got)
		}
	}
	cfg.Excluded = "preferred"
	for _, kind := range []string{"season", "still"} {
		if got := p.consistentSet(b, cfg, kind, "en"); got != "unlisted-complete" {
			t.Fatalf("%s ignored excluded creator: %q", kind, got)
		}
	}
	cfg.OnlyPriority = true
	for _, kind := range []string{"season", "still"} {
		if got := p.consistentSet(b, cfg, kind, "en"); got != "" {
			t.Fatalf("%s ignored OnlyPriority: %q", kind, got)
		}
	}
}

func TestConsistencyFallbackAndKeepExisting(t *testing.T) {
	p := newProvider()
	b := bundle{Sets: []set{
		{ID: "complete", Creator: "one", Art: []artwork{
			consistencyArt(40, "season", "complete", 1, 0, "en"),
			consistencyArt(41, "season", "complete", 2, 0, "en"),
			consistencyArt(43, "still", "complete", 1, 1, "en"),
			consistencyArt(44, "still", "complete", 1, 2, "en"),
		}},
		{ID: "single", Creator: "two", Art: []artwork{
			consistencyArt(42, "season", "single", 3, 0, "en"),
			consistencyArt(45, "still", "single", 1, 3, "en"),
		}},
	}}
	fallback := settings{SeasonConsistency: "MostComplete", TitlecardConsistency: "MostComplete", MissingArtwork: "Fallback"}
	for _, tc := range []struct {
		kind            string
		season, episode int
	}{
		{"season", 3, 0},
		{"still", 1, 3},
	} {
		got := p.automaticArt(b, fallback, tc.kind, tc.season, tc.episode, "en")
		if len(got) != 1 || got[0].SetID != "single" {
			t.Fatalf("%s fallback=%#v", tc.kind, got)
		}
	}
	strict := fallback
	strict.MissingArtwork = "KeepExisting"
	for _, tc := range []struct {
		kind            string
		season, episode int
	}{
		{"season", 3, 0},
		{"still", 1, 3},
	} {
		if got := p.automaticArt(b, strict, tc.kind, tc.season, tc.episode, "en"); len(got) != 0 {
			t.Fatalf("strict %s missing scope=%#v", tc.kind, got)
		}
	}
}

func TestConsistencyTieBreakingAndModes(t *testing.T) {
	p := newProvider()
	b := bundle{Sets: []set{
		{ID: "zeta", Creator: "same", GlobalRank: 92, Art: []artwork{
			consistencyArt(50, "poster", "zeta", 0, 0, "en"), consistencyArt(51, "season", "zeta", 1, 0, "en"), consistencyArt(52, "still", "zeta", 1, 1, "en"),
		}},
		{ID: "alpha", Creator: "same", GlobalRank: 92, Art: []artwork{
			consistencyArt(53, "poster", "alpha", 0, 0, "en"), consistencyArt(54, "season", "alpha", 1, 0, "en"), consistencyArt(55, "still", "alpha", 1, 1, "en"),
		}},
	}}
	most := settings{SeasonConsistency: "MostComplete", TitlecardConsistency: "MostComplete"}
	if got := p.consistentSet(b, most, "season", "en"); got != "alpha" {
		t.Fatalf("stable tie=%q, want alpha", got)
	}
	independent := settings{SeasonConsistency: "Independent", TitlecardConsistency: "Independent"}
	for _, tc := range []struct {
		kind            string
		season, episode int
	}{
		{"season", 1, 0},
		{"still", 1, 1},
	} {
		if got := p.automaticArt(b, independent, tc.kind, tc.season, tc.episode, "en"); len(got) != 1 || got[0].SetID != "zeta" {
			t.Fatalf("independent %s=%#v, want globally ranked zeta", tc.kind, got)
		}
	}
	legacy := settings{SeasonConsistency: "SeriesPoster", TitlecardConsistency: "SeriesPoster"}
	for _, tc := range []struct {
		kind            string
		season, episode int
	}{
		{"season", 1, 0},
		{"still", 1, 1},
	} {
		if got := p.automaticArt(b, legacy, tc.kind, tc.season, tc.episode, "en"); len(got) != 1 || got[0].SetID != "zeta" {
			t.Fatalf("legacy %s=%#v, want poster-anchor zeta", tc.kind, got)
		}
	}
}

func TestConfigureRejectsInvalidConsistencyEnums(t *testing.T) {
	s := &server{p: newProvider()}
	for _, values := range []map[string]any{
		{"api_token": "safe", "season_consistency": "set-whatever"},
		{"api_token": "safe", "titlecard_consistency": 7},
		{"api_token": "safe", "missing_artwork": "Replace"},
	} {
		value, err := structpb.NewStruct(values)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Configure(context.Background(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: value}}}); err == nil {
			t.Fatalf("invalid settings accepted: %#v", values)
		}
	}
}

func TestConfigureAcceptsCreatorPriorityConsistency(t *testing.T) {
	s := &server{p: newProvider()}
	value, err := structpb.NewStruct(map[string]any{
		"api_token":             "safe",
		"season_consistency":    "CreatorPriority",
		"titlecard_consistency": "CreatorPriority",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Configure(context.Background(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: value}}}); err != nil {
		t.Fatalf("CreatorPriority rejected: %v", err)
	}
	got := s.p.snapshot()
	if got.SeasonConsistency != "CreatorPriority" || got.TitlecardConsistency != "CreatorPriority" {
		t.Fatalf("configured consistency=%#v", got)
	}
}

func TestSeasonAndEpisodeRPCsUseSeparateMostCompleteSets(t *testing.T) {
	p := newProvider()
	p.configure(map[string]any{
		"api_token":             "safe",
		"season_consistency":    "MostComplete",
		"titlecard_consistency": "MostComplete",
		"missing_artwork":       "Fallback",
	})
	b := bundle{Sets: []set{
		{ID: "season-set", Creator: "seasons", GlobalRank: 100, Art: []artwork{
			consistencyArt(70, "season", "season-set", 1, 0, "en"),
			consistencyArt(71, "season", "season-set", 2, 0, "en"),
			consistencyArt(72, "still", "season-set", 1, 1, "en"),
		}},
		{ID: "titlecard-set", Creator: "cards", GlobalRank: 1, Art: []artwork{
			consistencyArt(73, "season", "titlecard-set", 1, 0, "en"),
			consistencyArt(74, "still", "titlecard-set", 1, 1, "en"),
			consistencyArt(75, "still", "titlecard-set", 1, 2, "en"),
		}},
	}}
	p.mu.Lock()
	p.cache[fmt.Sprintf("%d:series:42", p.generation)] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	p.mu.Unlock()
	s := &server{p: p}
	ids, err := structpb.NewStruct(map[string]any{"tmdb": "42"})
	if err != nil {
		t.Fatal(err)
	}
	seasons, err := s.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{ProviderIds: ids, Language: "en"})
	if err != nil || len(seasons.Seasons) != 2 {
		t.Fatalf("seasons=%#v err=%v", seasons, err)
	}
	for _, season := range seasons.Seasons {
		if !strings.Contains(season.PosterPath, consistencyID(70)) && !strings.Contains(season.PosterPath, consistencyID(71)) {
			t.Fatalf("season selected another set: %#v", season)
		}
	}
	episodes, err := s.GetEpisodes(context.Background(), &pluginv1.GetEpisodesRequest{ProviderIds: ids, SeasonNumber: 1, Language: "en"})
	if err != nil || len(episodes.Episodes) != 2 {
		t.Fatalf("episodes=%#v err=%v", episodes, err)
	}
	for _, episode := range episodes.Episodes {
		if !strings.Contains(episode.StillPath, consistencyID(74)) && !strings.Contains(episode.StillPath, consistencyID(75)) {
			t.Fatalf("episode selected another set: %#v", episode)
		}
	}
}
