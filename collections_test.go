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
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

const collectionMovieResponse = `{
  "data": {
    "movies_by_id": {
      "id": "429617",
      "movie_sets": [{
        "id": "23725", "popularity": 100, "user_created": {"username": "CmdrRiker"},
        "movie_poster": [{"id": "standalone-poster", "modified_on": "2026-10-11T01:02:03Z", "language": {"display_name": "English"}}],
        "movie_backdrop": [], "files": []
      }],
      "posters": [
        {"id": "collection-poster", "modified_on": "2026-10-10T01:02:03Z", "language": {"display_name": "English"}, "movie": {"id": "429617"}, "collection_set": {"id": "24304", "popularity": 1, "user_created": {"username": "willtong93"}}},
        {"id": "no-collection", "modified_on": "2026-10-10T01:02:03Z", "language": {"display_name": "English"}, "movie": {"id": "429617"}, "collection_set": null},
        {"id": "empty-collection", "modified_on": "2026-10-10T01:02:03Z", "language": {"display_name": "English"}, "movie": {"id": "429617"}, "collection_set": {"id": "", "user_created": {"username": "ignored"}}},
        {"id": "wrong-movie", "modified_on": "2026-10-10T01:02:03Z", "language": {"display_name": "English"}, "movie": {"id": "other"}, "collection_set": {"id": "wrong", "user_created": {"username": "ignored"}}},
        {"id": "null-movie", "modified_on": "2026-10-10T01:02:03Z", "language": {"display_name": "English"}, "movie": null, "collection_set": {"id": "null", "user_created": {"username": "ignored"}}}
      ],
      "backdrops": [
        {"id": "collection-backdrop", "modified_on": "2026-10-10T01:02:03Z", "language": {"display_name": ""}, "movie": {"id": "429617"}, "collection_set": {"id": "24304", "popularity": 1, "user_created": {"username": "willtong93"}}},
        {"id": "no-collection-backdrop", "modified_on": "2026-10-10T01:02:03Z", "language": {"display_name": "English"}, "movie": {"id": "429617"}, "collection_set": null}
      ]
    }
  }
}`

func TestMovieCollectionArtworkUsesCreatorRulesAndCache(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer safe" {
			t.Error("missing API token")
		}
		var request struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(request.Query, "collection_set") || !strings.Contains(request.Query, "posters") || !strings.Contains(request.Query, "backdrops") {
			t.Fatalf("movie query does not request collection artwork: %s", request.Query)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(collectionMovieResponse))
	}))
	defer upstream.Close()

	p := newProvider()
	p.endpoint = upstream.URL
	p.client = &http.Client{Timeout: time.Second}
	p.configure(map[string]any{
		"api_token":         "safe",
		"cache_hours":       24,
		"image_selection":   "BestOnly",
		"priority_creators": "willtong93",
	})
	s := &server{p: p}
	providerIDs, _ := structpb.NewStruct(map[string]any{"tmdb": "429617"})
	req := &pluginv1.GetImagesRequest{ItemType: "movie", ProviderIds: providerIDs, Language: "en"}

	automatic, err := s.GetImages(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(imageIDs(automatic), ","); got != "collection-poster,collection-backdrop" {
		t.Fatalf("automatic images = %q", got)
	}

	galleryCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(imageRequestPurposeHeader, "gallery"))
	gallery, err := s.GetImages(galleryCtx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(imageIDs(gallery), ","); got != "collection-poster,standalone-poster,collection-backdrop" {
		t.Fatalf("gallery images = %q", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("upstream calls = %d, want cache hit", calls.Load())
	}

	p.configure(map[string]any{
		"api_token":              "safe",
		"cache_hours":            24,
		"image_selection":        "BestOnly",
		"priority_creators":      "willtong93",
		"only_priority_creators": true,
	})
	onlyPriority, err := s.GetImages(galleryCtx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(imageIDs(onlyPriority), ","); got != "collection-poster,collection-backdrop" {
		t.Fatalf("OnlyPriority images = %q", got)
	}
	p.configure(map[string]any{
		"api_token":              "safe",
		"cache_hours":            24,
		"image_selection":        "BestOnly",
		"priority_creators":      "willtong93",
		"only_priority_creators": true,
		"excluded_creators":      "willtong93",
	})
	blocked, err := s.GetImages(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocked.GetImages()) != 0 {
		t.Fatalf("excluded collection creator returned images: %v", imageIDs(blocked))
	}
}

func TestParseMovieCollectionArtworkGroupsOnlyValidCollectionSets(t *testing.T) {
	var response struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(collectionMovieResponse), &response); err != nil {
		t.Fatal(err)
	}
	b, err := parseBundle(response.Data, "movie", "429617")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Sets) != 2 {
		t.Fatalf("sets = %#v", b.Sets)
	}
	byID := map[string]set{}
	for _, s := range b.Sets {
		byID[s.ID] = s
	}
	standalone, ok := byID["23725"]
	if !ok || standalone.Creator != "CmdrRiker" || len(standalone.Art) != 1 || standalone.Art[0].ID != "standalone-poster" {
		t.Fatalf("standalone movie artwork changed: %#v", standalone)
	}
	collection, ok := byID["24304"]
	if !ok || collection.Creator != "willtong93" || collection.Popularity != 1 || len(collection.Art) != 2 {
		t.Fatalf("collection set = %#v", collection)
	}
	if collection.Art[0].SetID != "24304" || collection.Art[0].Language != "en" || collection.Art[0].Modified.IsZero() {
		t.Fatalf("collection poster metadata = %#v", collection.Art[0])
	}
	for _, s := range b.Sets {
		for _, a := range s.Art {
			if a.ID == "no-collection" || a.ID == "empty-collection" || a.ID == "wrong-movie" || a.ID == "null-movie" || a.ID == "no-collection-backdrop" {
				t.Fatalf("invalid collection asset leaked: %#v", a)
			}
		}
	}
}

func TestMovieCollectionArtworkCompatibilityShapes(t *testing.T) {
	standalone := `[{"id":"direct","user_created":{"username":"direct"},"movie_poster":[{"id":"standalone"}],"movie_backdrop":[],"files":[]}]`
	collection := `[{"id":"collection","movie":{"id":"429617"},"collection_set":{"id":"24304","user_created":{"username":"willtong93"}}}]`
	for _, tc := range []struct {
		name                          string
		movieSets, posters, backdrops string
		wantSets                      int
		wantCollection                bool
		wantErr                       bool
	}{
		{"absent", standalone, "", "", 1, false, false},
		{"null", standalone, "null", "null", 1, false, false},
		{"empty", standalone, "[]", "[]", 1, false, false},
		{"collection-only", "[]", collection, "[]", 1, true, false},
		{"malformed", standalone, `{}`, "[]", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"movies_by_id":{"id":"429617","movie_sets":%s`, tc.movieSets)
			if tc.posters != "" {
				raw += `,"posters":` + tc.posters
			}
			if tc.backdrops != "" {
				raw += `,"backdrops":` + tc.backdrops
			}
			raw += `}}`
			b, err := parseBundle(json.RawMessage(raw), "movie", "429617")
			if (err != nil) != tc.wantErr {
				t.Fatalf("parse err = %v", err)
			}
			if tc.wantErr {
				return
			}
			if len(b.Sets) != tc.wantSets {
				t.Fatalf("sets = %#v", b.Sets)
			}
			_, hasCollection := func() (set, bool) {
				for _, s := range b.Sets {
					if s.ID == "24304" {
						return s, true
					}
				}
				return set{}, false
			}()
			if hasCollection != tc.wantCollection {
				t.Fatalf("collection set present = %t", hasCollection)
			}
		})
	}
}
