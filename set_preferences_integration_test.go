package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestSavedSetNarrowsAutomaticButNotGallery(t *testing.T) {
	s := testSettingsServer(t, nil)
	s.setBindings = map[string]string{"series:1399": "2"}
	b := bundle{Sets: []set{{ID: "1", Art: []artwork{{ID: "first", SetID: "1", Kind: "poster"}}}, {ID: "2", Art: []artwork{{ID: "chosen", SetID: "2", Kind: "poster"}}}}}
	narrowed := s.preferredBundle("series", "1399", b)
	if len(narrowed.Sets) != 1 || narrowed.Sets[0].ID != "2" {
		t.Fatalf("preference ignored: %#v", narrowed)
	}
	if len(b.Sets) != 2 {
		t.Fatal("preference mutated shared cache")
	}
	if len(s.preferredBundle("series", "999", b).Sets) != 2 {
		t.Fatal("unbound target narrowed")
	}
	s.setBindings["series:1399"] = "missing"
	if len(s.preferredBundle("series", "1399", b).Sets) != 0 {
		t.Fatal("missing pinned set fell back to unrelated artwork")
	}
}

func TestConfigureRestoresSetBindings(t *testing.T) {
	s := testSettingsServer(t, nil)
	settings, _ := structpb.NewStruct(map[string]any{"api_token": "safe"})
	bindings, _ := structpb.NewStruct(map[string]any{"bindings": map[string]any{"movie:429617": "123", "series:1399": "456", "collection:local-1": "789"}})
	_, err := s.Configure(context.Background(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: settings}, {Key: "set_preferences", Value: bindings}}})
	if err != nil || s.preferredSet("movie", "429617") != "123" {
		t.Fatalf("restore failed: %v", err)
	}
	_, err = s.Configure(context.Background(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: settings}}})
	if err != nil || s.preferredSet("movie", "429617") != "" {
		t.Fatal("removed configuration retained stale binding")
	}
}

func TestBoundProviderGalleryRemainsComplete(t *testing.T) {
	s := testSettingsServer(t, nil)
	s.p.configure(map[string]any{"api_token": "safe", "cache_hours": 24, "image_selection": "All"})
	id1 := "295df690-c7c9-42f5-8496-0d2088c521f1"
	id2 := "ec1c004a-ed29-4406-9d69-c8cfe277e0cc"
	b := bundle{Sets: []set{{ID: "1", Art: []artwork{{ID: id1, SetID: "1", Kind: "poster"}}}, {ID: "2", Art: []artwork{{ID: id2, SetID: "2", Kind: "poster"}}}}}
	s.p.cache[fmt.Sprintf("%d:movie:429617", s.p.generation)] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	s.setBindings = map[string]string{"movie:429617": "2"}
	req := &pluginv1.GetImagesRequest{ItemType: "movie", ProviderId: "movie:429617"}
	out, err := s.GetImages(context.Background(), req)
	if err != nil || len(out.GetImages()) != 1 || !strings.Contains(out.Images[0].Url, id2) {
		t.Fatalf("automatic binding not applied: %#v %v", out, err)
	}
	gallery := metadata.NewIncomingContext(context.Background(), metadata.Pairs(imageRequestPurposeHeader, "gallery"))
	out, err = s.GetImages(gallery, req)
	if err != nil || len(out.GetImages()) != 2 {
		t.Fatalf("gallery lost alternatives: %#v %v", out, err)
	}
}

func TestSavedShowSetControlsSeasonsAndEpisodes(t *testing.T) {
	s := testSettingsServer(t, nil)
	s.p.configure(map[string]any{"api_token": "safe", "cache_hours": 24})
	b := bundle{Sets: []set{
		{ID: "1", Art: []artwork{{ID: "295df690-c7c9-42f5-8496-0d2088c521f1", SetID: "1", Kind: "season", Season: 1}, {ID: "ec1c004a-ed29-4406-9d69-c8cfe277e0cc", SetID: "1", Kind: "still", Season: 1, Episode: 1}}},
		{ID: "2", Art: []artwork{{ID: "1c3ba710-ec6e-4aaf-8c4c-57923070d2e9", SetID: "2", Kind: "season", Season: 1}, {ID: "01dd338d-230c-44de-82db-770c69b97609", SetID: "2", Kind: "still", Season: 1, Episode: 1}}},
	}}
	s.p.cache[fmt.Sprintf("%d:series:1399", s.p.generation)] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	s.setBindings = map[string]string{"series:1399": "2"}
	seasons, err := s.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{SeriesProviderId: "series:1399"})
	if err != nil || len(seasons.Seasons) != 1 || !strings.Contains(seasons.Seasons[0].PosterPath, "1c3ba710") {
		t.Fatalf("season ignored binding: %#v %v", seasons, err)
	}
	episodes, err := s.GetEpisodes(context.Background(), &pluginv1.GetEpisodesRequest{SeriesProviderId: "series:1399", SeasonNumber: 1})
	if err != nil || len(episodes.Episodes) != 1 || !strings.Contains(episodes.Episodes[0].StillPath, "01dd338d") {
		t.Fatalf("episode ignored binding: %#v %v", episodes, err)
	}
	s.setBindings["series:1399"] = "999"
	seasons, err = s.GetSeasons(context.Background(), &pluginv1.GetSeasonsRequest{SeriesProviderId: "series:1399"})
	if err != nil || len(seasons.Seasons) != 0 {
		t.Fatal("missing saved set substituted unrelated season")
	}
}
