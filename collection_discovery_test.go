package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollectionSourceIdentityDoesNotExposeSourceSecrets(t *testing.T) {
	var item collectionRecord
	if err := json.Unmarshal([]byte(`{"id":"42","source_config":{"mode":"tmdb_collection","collection_id":531241,"api_key":"do-not-expose"}}`), &item); err != nil {
		t.Fatal(err)
	}
	if item.TMDBCollectionID != "531241" {
		t.Fatal("native franchise identity missing")
	}
	b, _ := json.Marshal(item)
	if strings.Contains(string(b), "do-not-expose") {
		t.Fatal("raw source configuration exposed")
	}
}

func TestCollectionDiscoveryUsesExactIdentityBeforeMembers(t *testing.T) {
	var queries []string
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/admin/collections/42":
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`{"id":"42","title":"Renamed collection","source_config":{"mode":"tmdb_collection","collection_id":531241}}`))
		case "/api/v2/admin/collections":
			_, _ = w.Write([]byte(`{"items":[{"id":"42","title":"Renamed collection"}]}`))
		default:
			t.Errorf("unexpected native read: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		queries = append(queries, in["query"].(string))
		_, _ = w.Write([]byte(`{"data":{"collections_by_id":{"id":"531241","collection_name":"Spider-Man","posters":[],"backdrops":[]}}}`))
	}))
	defer upstream.Close()
	s := collectionTestServer(native.URL, "safe")
	s.p.configure(map[string]any{"api_token": "safe"})
	s.p.endpoint = upstream.URL
	r, e := s.Handle(context.Background(), collectionRequestFor("GET", "/collections/discover", "admin", "", map[string]any{"id": "42"}))
	if e != nil || r.StatusCode != 200 {
		t.Fatalf("status %d error %v", r.StatusCode, e)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], "collections_by_id") {
		t.Fatal("discovery used title or member fallback despite exact identity")
	}
}

func TestCollectionDiscoveryUsesProfileScopedMemberIdentity(t *testing.T) {
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/admin/collections/42":
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`{"id":"42","title":"Custom","library_ids":["7"]}`))
		case "/api/v2/admin/collections":
			_, _ = w.Write([]byte(`{"items":[{"id":"42","title":"Custom"}]}`))
		case "/api/v2/profiles":
			_, _ = w.Write([]byte(`{"items":[{"id":"primary","is_primary":true}]}`))
		case "/api/v2/library/7/collections/42/items":
			if r.Header.Get("X-Profile-Id") != "primary" || r.URL.Query().Get("limit") != "8" {
				t.Error("bounded member profile missing")
			}
			_, _ = w.Write([]byte(`{"items":[{"content_id":"movie-1","type":"movie"}],"page":{"has_more":true}}`))
		case "/api/v2/catalog/items/movie-1":
			if r.Header.Get("X-Profile-Id") != "primary" {
				t.Error("detail profile missing")
			}
			_, _ = w.Write([]byte(`{"tmdb_id":"429617"}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"movies_by_id":{"collection_id":{"id":"531241","collection_name":"Spider-Man","posters":[],"backdrops":[]}}}}`))
	}))
	defer upstream.Close()
	s := collectionTestServer(native.URL, "safe")
	s.p.configure(map[string]any{"api_token": "safe"})
	s.p.endpoint = upstream.URL
	r, e := s.Handle(context.Background(), collectionRequestFor("GET", "/collections/discover", "admin", "", map[string]any{"id": "42"}))
	if e != nil || r.StatusCode != 200 || !strings.Contains(string(r.Body), `"basis":"member_tmdb"`) || !strings.Contains(string(r.Body), `"has_more":true`) {
		t.Fatalf("member lookup %v %v", r, e)
	}
}

func TestHiddenManualDiscoveryUsesAdminMembers(t *testing.T) {
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/admin/collections/42":
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`{"id":"42","title":"Hidden","collection_type":"manual","library_ids":["7"]}`))
		case "/api/v2/admin/collections":
			_, _ = w.Write([]byte(`{"items":[{"id":"42","title":"Hidden"}]}`))
		case "/api/v2/profiles":
			_, _ = w.Write([]byte(`{"items":[{"id":"primary","is_primary":true}]}`))
		case "/api/v2/admin/collections/42/items":
			_, _ = w.Write([]byte(`{"items":[{"media_item_id":"movie-1"}],"page":{"has_more":false}}`))
		case "/api/v2/catalog/items/movie-1":
			_, _ = w.Write([]byte(`{"tmdb_id":"429617","type":"movie"}`))
		default:
			t.Errorf("hidden collection used viewer route: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"movies_by_id":{"collection_id":{"id":"531241","collection_name":"Spider-Man","posters":[],"backdrops":[]}}}}`))
	}))
	defer upstream.Close()
	s := collectionTestServer(native.URL, "safe")
	s.p.configure(map[string]any{"api_token": "safe"})
	s.p.endpoint = upstream.URL
	r, e := s.Handle(context.Background(), collectionRequestFor("GET", "/collections/discover", "admin", "", map[string]any{"id": "42"}))
	if e != nil || r.StatusCode != 200 {
		t.Fatalf("hidden manual discovery failed: %v %v", r, e)
	}
}
