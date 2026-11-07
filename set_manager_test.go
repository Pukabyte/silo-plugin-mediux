package main

import (
	"context"
	"encoding/json"
	"fmt"
	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func setReq(method, path, body string, q map[string]any) *pluginv1.HandleHTTPRequest {
	v, _ := structpb.NewStruct(q)
	return &pluginv1.HandleHTTPRequest{Method: method, Path: path, Body: []byte(body), Query: v, Headers: map[string]string{"X-Silo-User-Role": "admin"}}
}
func setTestServer(save func(context.Context, map[string]string) error) *server {
	s := &server{p: newProvider(), settingsCSRF: "csrf", setBindings: map[string]string{}, saveSetPreferences: save}
	s.p.configure(map[string]any{"api_token": "token"})
	s.p.cache = map[string]cacheEntry{fmt.Sprintf("%d:movie:1", s.p.generation): {b: bundle{Sets: []set{{ID: "10", Title: "Ten", Creator: "Good", Art: []artwork{{ID: "295df690-c7c9-42f5-8496-0d2088c521f1", Kind: "poster", SetID: "10"}, {ID: "ec1c004a-ed29-4406-9d69-c8cfe277e0cc", Kind: "still", SetID: "10"}, {ID: "1c3ba710-ec6e-4aaf-8c4c-57923070d2e9", Kind: "logo", SetID: "10"}}}, {ID: "20", Title: "Twenty", Creator: "Bad", Art: []artwork{{ID: "01dd338d-230c-44de-82db-770c69b97609", Kind: "poster", SetID: "20"}}}}}, until: time.Now().Add(time.Hour)}}
	return s
}

func TestSetPreferenceRejectsCSRFBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer up.Close()
	s := setTestServer(func(context.Context, map[string]string) error { t.Fatal("save called"); return nil })
	s.p.endpoint = up.URL
	r, e := s.handleSets(context.Background(), setReq(http.MethodPut, "/sets/preferences", `{"csrf":"wrong","type":"movie","id":"1","set_id":"10"}`, nil))
	if e != nil || r.GetStatusCode() != http.StatusForbidden || calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d err=%v", r.GetStatusCode(), calls.Load(), e)
	}
}

func TestSetPreferenceRejectsTrailingJSONAndCrossItem(t *testing.T) {
	s := setTestServer(nil)
	for _, body := range []string{`{"csrf":"csrf","type":"movie","id":"1","set_id":"10"}{}`, `{"csrf":"csrf","type":"movie","id":"1","set_id":"999"}`} {
		r, e := s.handleSets(context.Background(), setReq(http.MethodPut, "/sets/preferences", body, nil))
		if e != nil || r.GetStatusCode() != http.StatusBadRequest {
			t.Fatalf("status=%d err=%v", r.GetStatusCode(), e)
		}
	}
}

func TestSetPreferencePersistenceRollbackAndDeleteWithoutLookup(t *testing.T) {
	s := setTestServer(func(context.Context, map[string]string) error { return nil })
	r, e := s.handleSets(context.Background(), setReq(http.MethodPut, "/sets/preferences", `{"csrf":"csrf","type":"movie","id":"1","set_id":"10"}`, nil))
	if e != nil || r.GetStatusCode() != 200 {
		t.Fatalf("save status=%d err=%v", r.GetStatusCode(), e)
	}
	if s.preferredSet("movie", "1") != "10" {
		t.Fatal("binding not saved")
	}
	s.saveSetPreferences = func(context.Context, map[string]string) error { return context.DeadlineExceeded }
	r, e = s.handleSets(context.Background(), setReq(http.MethodPut, "/sets/preferences", `{"csrf":"csrf","type":"movie","id":"1","set_id":""}`, nil))
	if e != nil || r.GetStatusCode() != 500 || s.preferredSet("movie", "1") != "10" {
		t.Fatalf("rollback failed status=%d err=%v", r.GetStatusCode(), e)
	}
	s.saveSetPreferences = func(context.Context, map[string]string) error { return nil }
	r, e = s.handleSets(context.Background(), setReq(http.MethodPut, "/sets/preferences", `{"csrf":"csrf","type":"movie","id":"1","set_id":""}`, nil))
	if e != nil || r.GetStatusCode() != 200 || s.preferredSet("movie", "1") != "" {
		t.Fatalf("delete failed status=%d err=%v", r.GetStatusCode(), e)
	}
}

func TestSetCatalogKindsAndExclusions(t *testing.T) {
	s := setTestServer(nil)
	s.p.cfg.Excluded = "Bad"
	r, e := s.handleSets(context.Background(), setReq(http.MethodGet, "/sets/catalog", "", map[string]any{"type": "movie", "id": "1"}))
	if e != nil || r.GetStatusCode() != 200 {
		t.Fatalf("status=%d err=%v", r.GetStatusCode(), e)
	}
	var out struct {
		Sets []setCatalogSet `json:"sets"`
	}
	if json.Unmarshal(r.GetBody(), &out) != nil || len(out.Sets) != 1 || len(out.Sets[0].Artwork) != 3 {
		t.Fatalf("unexpected catalog: %s", r.GetBody())
	}
	kinds := map[string]bool{}
	for _, a := range out.Sets[0].Artwork {
		kinds[a.Kind] = true
	}
	for _, k := range []string{"poster", "still", "logo"} {
		if !kinds[k] {
			t.Fatalf("missing kind %s", k)
		}
	}
}

func TestSetPreferenceRequiresAdmin(t *testing.T) {
	s := setTestServer(nil)
	req := setReq(http.MethodGet, "/sets/preferences", "", nil)
	req.Headers["X-Silo-User-Role"] = "member"
	r, e := s.handleSets(context.Background(), req)
	if e != nil || r.GetStatusCode() != 403 {
		t.Fatalf("status=%d err=%v", r.GetStatusCode(), e)
	}
}

func TestSetCatalogPagesAllImagesWithoutHidingNextSet(t *testing.T) {
	s := setTestServer(func(context.Context, map[string]string) error { return nil })
	key := fmt.Sprintf("%d:movie:1", s.p.generation)
	b := s.p.cache[key].b
	for i := 0; i < 1100; i++ {
		b.Sets[0].Art = append(b.Sets[0].Art, artwork{ID: "295df690-c7c9-42f5-8496-0d2088c521f1", Kind: "still", SetID: "10", Season: 1, Episode: i + 1})
	}
	s.p.cache[key] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	result, err := s.setCatalog(context.Background(), "movie", "1")
	if err != nil || len(result.Sets) != 2 || !result.HasMore || len(result.Sets[0].Artwork) != 1000 || result.Sets[0].ArtworkCount < 1100 {
		t.Fatalf("unbounded or incomplete set previews: %#v %v", result, err)
	}
	next, err := s.setCatalogPage(context.Background(), "movie", "1", result.NextOffset)
	if err != nil || next.NextOffset != 0 || len(next.Sets[0].Artwork) != 103 || len(next.Sets[1].Artwork) != 1 {
		t.Fatalf("missing artwork page: %#v %v", next, err)
	}

}

func TestSetCatalogIncludesEverySeasonImage(t *testing.T) {
	s := setTestServer(nil)
	key := fmt.Sprintf("%d:movie:1", s.p.generation)
	b := s.p.cache[key].b
	b.Sets[0].Art = nil
	for i := 0; i < 61; i++ {
		b.Sets[0].Art = append(b.Sets[0].Art, artwork{ID: "295df690-c7c9-42f5-8496-0d2088c521f1", Kind: "still", SetID: "10", Season: i%3 + 1, Episode: i/3 + 1})
	}
	s.p.cache[key] = cacheEntry{b: b, until: time.Now().Add(time.Hour)}
	result, err := s.setCatalog(context.Background(), "movie", "1")
	if err != nil || len(result.Sets[0].Artwork) != 61 {
		t.Fatalf("missing series images: %v %v", len(result.Sets[0].Artwork), err)
	}
}
