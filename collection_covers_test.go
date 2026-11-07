package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func coverRequest(role, query string) *pluginv1.HandleHTTPRequest {
	q, _ := structpb.NewStruct(map[string]any{"q": query})
	return &pluginv1.HandleHTTPRequest{Method: "GET", Path: "/collection-artwork", Query: q, Headers: map[string]string{"X-Silo-User-Role": role}}
}

func coverSelectorRequest(role string, selectors map[string]any) *pluginv1.HandleHTTPRequest {
	q, _ := structpb.NewStruct(selectors)
	return &pluginv1.HandleHTTPRequest{Method: "GET", Path: "/collection-artwork", Query: q, Headers: map[string]string{"X-Silo-User-Role": role}}
}

func TestCollectionCoversRemoteReferences(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-token" {
			t.Error("missing saved credential")
		}
		var body struct {
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Variables["query"] != "Spider-Man" {
			t.Errorf("wrong query: %+v", body.Variables)
		}
		fmt.Fprint(w, `{"data":{"collections":[{"id":"125574","collection_name":"The Amazing Spider-Man Collection","collection_sets":[{"id":"2495","set_title":"Matching collection","popularity":10,"user_created":{"username":"Creator"},"collection_poster":[{"id":"afea59e5-d5a5-4eae-afd2-3d9441e7d407","modified_on":"2024-01-14T04:52:57.000Z","width":1000,"height":1500,"movie":null},{"id":"bad-id"},{"id":"f63700a9-306b-4c0d-b4f5-5cbfb64c590c","movie":{"id":"1930"}}],"collection_backdrop":[{"id":"f63700a9-306b-4c0d-b4f5-5cbfb64c590c","modified_on":"2024-01-28T08:07:11.000Z","movie":null}]}]}]}}`)
	}))
	defer upstream.Close()
	p := newProvider()
	p.endpoint = upstream.URL
	p.configure(map[string]any{"api_token": "secret-token"})
	s := &server{p: p}
	response, err := s.Handle(context.Background(), coverRequest("admin", " Spider-Man "))
	if err != nil || response.GetStatusCode() != 200 {
		t.Fatalf("status=%v err=%v", response, err)
	}
	var result struct {
		Items []struct {
			ID, Kind, URL, Path, Creator, Title string
			SetTitle                            string `json:"set_title"`
			CollectionID                        string `json:"collection_id"`
			Width, Height                       int
		}
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("only actual collection covers expected: %s", response.Body)
	}
	poster := result.Items[0]
	if poster.Kind != "poster" || poster.Path != "" || poster.URL != "https://images.mediux.io/assets/afea59e5-d5a5-4eae-afd2-3d9441e7d407?key=jpg&v=20240114045257" {
		t.Fatalf("unstable remote reference: %+v", poster)
	}
	if poster.Creator != "Creator" || poster.Width != 1000 || poster.Height != 1500 {
		t.Fatalf("missing preview metadata: %+v", poster)
	}
	if poster.SetTitle != "Matching collection" || poster.CollectionID != "125574" {
		t.Fatalf("missing set grouping metadata: %+v", poster)
	}
	if strings.Contains(string(response.Body), "secret-token") {
		t.Fatal("credential exposed")
	}
}

func TestCollectionCoverSelectorsAndMemberAssets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		switch {
		case body.Variables["id"] == "777" && strings.Contains(body.Query, "movies_by_id"):
			fmt.Fprint(w, `{"data":{"movies_by_id":{"collection_id":{"id":"42","collection_name":"Collection","posters":[{"id":"afea59e5-d5a5-4eae-afd2-3d9441e7d407","modified_on":"2024-01-14T04:52:57Z","collection_set":{"id":"25","set_title":"Good","popularity":2,"user_created":{"username":"Creator"}}},{"id":"f63700a9-306b-4c0d-b4f5-5cbfb64c590c","movie":{"id":"777"},"collection_set":{"id":"25","set_title":"Good","user_created":{"username":"Creator"}}}]}}}}`)
		case body.Variables["id"] == "25" && strings.Contains(body.Query, "collection_sets_by_id"):
			fmt.Fprint(w, `{"data":{"collection_sets_by_id":{"id":"25","set_title":"Good","popularity":2,"user_created":{"username":"Creator"},"collection_id":{"id":"42","collection_name":"Collection"},"collection_poster":[{"id":"afea59e5-d5a5-4eae-afd2-3d9441e7d407"}],"collection_backdrop":[]}}}`)
		default:
			t.Fatalf("unexpected request: %+v", body)
		}
	}))
	defer upstream.Close()
	p := newProvider()
	p.endpoint = upstream.URL
	p.configure(map[string]any{"api_token": "token"})
	s := &server{p: p}
	member, err := s.Handle(context.Background(), coverSelectorRequest("admin", map[string]any{"movie": "777"}))
	if err != nil || member.GetStatusCode() != 200 || strings.Contains(string(member.Body), "f63700a9") || !strings.Contains(string(member.Body), `"collection_id":"42"`) {
		t.Fatalf("member result=%s err=%v", member.Body, err)
	}
	byURL, err := s.Handle(context.Background(), coverSelectorRequest("admin", map[string]any{"set": "https://mediux.pro/sets/25"}))
	if err != nil || byURL.GetStatusCode() != 200 || !strings.Contains(string(byURL.Body), `"set_id":"25"`) {
		t.Fatalf("set URL result=%s err=%v", byURL.Body, err)
	}
	for _, value := range []map[string]any{{"set": "http://mediux.pro/sets/25"}, {"set": "https://mediux.pro/sets/25?x=1"}, {"set": "https://mediux.pro/sets/not-a-number"}, {"q": "Action", "tmdb": "1"}, {"tmdb": "0"}} {
		response, err := s.Handle(context.Background(), coverSelectorRequest("admin", value))
		if err != nil || response.GetStatusCode() != 400 {
			t.Fatalf("selector=%v status=%v err=%v", value, response, err)
		}
	}
}

func TestCollectionCoverAccessAndFailures(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"errors":[{"message":"upstream secret-token"}]}`)
	}))
	defer upstream.Close()
	p := newProvider()
	p.endpoint = upstream.URL
	p.configure(map[string]any{"api_token": "secret-token"})
	s := &server{p: p}
	for _, tc := range []struct {
		role, query string
		status      int
	}{{"user", "Action", 403}, {"admin", "a", 400}, {"admin", strings.Repeat("x", 101), 400}, {"admin", "Action", 502}} {
		response, err := s.Handle(context.Background(), coverRequest(tc.role, tc.query))
		if err != nil || int(response.GetStatusCode()) != tc.status {
			t.Fatalf("role=%s query=%q response=%v err=%v", tc.role, tc.query, response, err)
		}
		if strings.Contains(string(response.Body), "secret-token") {
			t.Fatal("upstream credential exposed")
		}
	}
	if calls != 1 {
		t.Fatalf("invalid requests reached upstream: %d", calls)
	}
}

func TestCollectionCoverRankingAndEmpty(t *testing.T) {
	for _, responseBody := range []string{`{"data":{"collections":[]}}`, `{"data":{"collections":[{"id":"1","collection_name":"Action","collection_sets":[{"id":"1","user_created":{"username":"Excluded"},"collection_poster":[{"id":"afea59e5-d5a5-4eae-afd2-3d9441e7d407"}]},{"id":"2","user_created":{"username":"Other"},"collection_poster":[{"id":"f63700a9-306b-4c0d-b4f5-5cbfb64c590c"}]}]}]}}`} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, responseBody) }))
		p := newProvider()
		p.endpoint = upstream.URL
		p.configure(map[string]any{"api_token": "token", "excluded_creators": "excluded", "only_priority_creators": true, "priority_creators": "Wanted"})
		response, err := (&server{p: p}).Handle(context.Background(), coverRequest("admin", "Action"))
		upstream.Close()
		if err != nil || response.GetStatusCode() != 200 || !strings.Contains(string(response.Body), `"items":[]`) {
			t.Fatalf("unexpected fallback or excluded cover: %v %v", response, err)
		}
	}
}
