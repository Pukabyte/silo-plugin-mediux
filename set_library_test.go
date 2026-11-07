package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetLibraryUsesBoundedSearchAndIdentity(t *testing.T) {
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/profiles":
			_, _ = w.Write([]byte(`{"items":[{"id":"primary","is_primary":true,"is_child":false,"has_pin":false}]}`))
		case "/api/v2/catalog":
			if r.Header.Get("X-Profile-Id") != "primary" {
				t.Error("catalog profile missing")
			}
			if r.URL.Query().Get("q") != "Spider-Man & friends" || r.URL.Query().Get("limit") != "20" {
				t.Errorf("query escaping or limit broken: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"items":[{"content_id":"movie-tmdb-429617","type":"movie","title":"Spider-Man","year":2019}],"page":{"has_more":true}}`))
		case "/api/v2/catalog/items/movie-tmdb-429617":
			_, _ = w.Write([]byte(`{"content_id":"movie-tmdb-429617","type":"movie","title":"Spider-Man","tmdb_id":"429617"}`))
		default:
			t.Errorf("unexpected native call %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	s := collectionTestServer(native.URL, "safe")
	res, err := s.Handle(context.Background(), collectionRequestFor("GET", "/sets/items", "admin", "", map[string]any{"type": "movie", "q": "Spider-Man & friends"}))
	if err != nil || res.StatusCode != 200 || !strings.Contains(string(res.Body), `"has_more":true`) {
		t.Fatalf("search %v %v", res, err)
	}
	res, err = s.Handle(context.Background(), collectionRequestFor("GET", "/sets/item", "admin", "", map[string]any{"id": "movie-tmdb-429617"}))
	if err != nil || res.StatusCode != 200 || !strings.Contains(string(res.Body), `"id":"429617"`) {
		t.Fatalf("identity %v %v", res, err)
	}
}

func TestSetLibraryNeverUsesPINOrChildProfile(t *testing.T) {
	calls := 0
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v2/profiles" {
			t.Error("used an ineligible profile")
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"locked","is_primary":true,"has_pin":true},{"id":"child","is_primary":true,"is_child":true}]}`))
	}))
	defer native.Close()
	s := collectionTestServer(native.URL, "safe")
	_, _, _, err := s.catalogRead(context.Background(), "/api/v2/catalog?limit=20")
	if err == nil || calls != 1 {
		t.Fatalf("ineligible profile accepted: %v", err)
	}
}
