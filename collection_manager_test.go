package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func collectionTestServer(baseURL, key string) *server {
	return &server{p: newProvider(), collectionKey: key, settingsCSRF: "csrf-token", collectionBaseURL: func(context.Context) (string, error) { return baseURL, nil }}
}

func collectionRequestFor(method, path, role, body string, query map[string]any) *pluginv1.HandleHTTPRequest {
	q, _ := structpb.NewStruct(query)
	return &pluginv1.HandleHTTPRequest{Method: method, Path: path, Headers: map[string]string{"X-Silo-User-Role": role}, Body: []byte(body), Query: q}
}

func TestCollectionRoutesRequireAdminAndConfiguredConnection(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	s := collectionTestServer(upstream.URL, "secret-collection-key")
	response, err := s.Handle(context.Background(), collectionRequestFor(http.MethodGet, "/collections/catalog", "member", "", nil))
	if err != nil || response.GetStatusCode() != http.StatusForbidden || calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d err=%v", response.GetStatusCode(), calls.Load(), err)
	}
	s.collectionKey = ""
	response, err = s.Handle(context.Background(), collectionRequestFor(http.MethodGet, "/collections/catalog", "admin", "", nil))
	if err != nil || response.GetStatusCode() != http.StatusServiceUnavailable || calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d err=%v", response.GetStatusCode(), calls.Load(), err)
	}
	if strings.Contains(string(response.GetBody()), "secret-collection-key") {
		t.Fatal("collection key exposed")
	}
}

func TestCollectionCoverRejectsBadCSRFBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	s := collectionTestServer(upstream.URL, "secret-collection-key")
	body := `{"csrf":"wrong","id":"42","slot":"poster","url":"https://images.example/cover.jpg","etag":"\"v1\""}`
	response, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/collections/cover", "admin", body, nil))
	if err != nil || response.GetStatusCode() != http.StatusForbidden || calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d err=%v", response.GetStatusCode(), calls.Load(), err)
	}
}

func TestCollectionSnapshotMergesListedArtworkAndETag(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-collection-key" {
			t.Error("dedicated connection key missing")
		}
		switch r.URL.Path {
		case "/api/v2/admin/collections":
			_, _ = w.Write([]byte(`{"items":[{"id":"42","title":"Collection","poster_url":"https://images.example/poster.jpg","backdrop_url":"https://images.example/backdrop.jpg","library_ids":["1"],"item_count":3}]}`))
		case "/api/v2/admin/collections/42":
			w.Header().Set("ETag", `"v2"`)
			_, _ = w.Write([]byte(`{"id":"42","title":"Collection","poster_url":"","backdrop_url":"","library_ids":["1"],"item_count":3}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	s := collectionTestServer(upstream.URL, "secret-collection-key")
	response, err := s.Handle(context.Background(), collectionRequestFor(http.MethodGet, "/collections/item", "admin", "", map[string]any{"id": "42"}))
	if err != nil || response.GetStatusCode() != http.StatusOK {
		t.Fatalf("status=%d err=%v body=%s", response.GetStatusCode(), err, response.GetBody())
	}
	var payload struct {
		Collection collectionRecord `json:"collection"`
		ETag       string           `json:"etag"`
	}
	if err := json.Unmarshal(response.GetBody(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.ETag != `"v2"` || payload.Collection.PosterURL != "https://images.example/poster.jpg" || payload.Collection.BackdropURL != "https://images.example/backdrop.jpg" {
		t.Fatalf("unexpected snapshot: %+v", payload)
	}
}

func TestCollectionCoverPatchesOneRemoteFieldAndReadbacks(t *testing.T) {
	var patches atomic.Int32
	selected := "https://images.example/selected.jpg"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/admin/collections/42":
			if r.Method == http.MethodPatch {
				patches.Add(1)
				if r.Header.Get("If-Match") != `"v1"` {
					t.Errorf("If-Match=%q", r.Header.Get("If-Match"))
				}
				var body map[string]string
				_ = json.NewDecoder(r.Body).Decode(&body)
				if len(body) != 1 || body["poster_url"] != selected || body["backdrop_url"] != "" {
					t.Errorf("unexpected patch: %#v", body)
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("ETag", `"v2"`)
			_, _ = w.Write([]byte(`{"id":"42","title":"Collection","poster_url":"","backdrop_url":"","library_ids":[],"item_count":1}`))
		case "/api/v2/admin/collections":
			_, _ = w.Write([]byte(`{"items":[{"id":"42","title":"Collection","poster_url":"https://images.example/selected.jpg","backdrop_url":"","library_ids":[],"item_count":1}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	s := collectionTestServer(upstream.URL, "secret-collection-key")
	body := `{"csrf":"csrf-token","id":"42","slot":"poster","url":"https://images.example/selected.jpg","etag":"\"v1\""}`
	response, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/collections/cover", "admin", body, nil))
	if err != nil || response.GetStatusCode() != http.StatusOK || patches.Load() != 1 {
		t.Fatalf("status=%d patches=%d err=%v body=%s", response.GetStatusCode(), patches.Load(), err, response.GetBody())
	}
	if strings.Contains(string(response.GetBody()), "secret-collection-key") {
		t.Fatal("collection key exposed")
	}
}

func TestCollectionCoverStaleAndReadbackFailureDoNotClaimSuccess(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"stale": func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusPreconditionFailed) },
		"readback": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPatch {
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Path == "/api/v2/admin/collections" {
				_, _ = w.Write([]byte(`{"items":[{"id":"42","title":"Collection","poster_url":"https://images.example/other.jpg"}]}`))
				return
			}
			w.Header().Set("ETag", `"v2"`)
			_, _ = w.Write([]byte(`{"id":"42","title":"Collection"}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			var patches atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPatch {
					patches.Add(1)
				}
				handler(w, r)
			}))
			defer upstream.Close()
			s := collectionTestServer(upstream.URL, "key")
			body := `{"csrf":"csrf-token","id":"42","slot":"poster","url":"https://images.example/selected.jpg","etag":"\"v1\""}`
			response, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/collections/cover", "admin", body, nil))
			want := int32(http.StatusBadGateway)
			if name == "stale" {
				want = http.StatusPreconditionFailed
			}
			if err != nil || response.GetStatusCode() != want || patches.Load() != 1 {
				t.Fatalf("status=%d patches=%d err=%v body=%s", response.GetStatusCode(), patches.Load(), err, response.GetBody())
			}
		})
	}
}

func TestCollectionRequestBoundsRedirectAndCancellation(t *testing.T) {
	if _, err := decodeCollectionCoverPatch([]byte(`{"csrf":"x","id":"../42","slot":"poster","url":"https://a.example/x","etag":"\"v1\""}`)); err == nil {
		t.Fatal("path traversal accepted")
	}
	if validRemoteArtworkURL("https://user:pass@example.test/a") || validRemoteArtworkURL("https://example.test/a#fragment") || validETag("*") || validETag(`W/"v1"`) {
		t.Fatal("unsafe URL or ETag accepted")
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.test/elsewhere", http.StatusFound)
	}))
	defer redirect.Close()
	s := collectionTestServer(redirect.URL, "key")
	response, err := s.Handle(context.Background(), collectionRequestFor(http.MethodGet, "/collections/catalog", "admin", "", nil))
	if err != nil || response.GetStatusCode() != http.StatusBadGateway {
		t.Fatalf("redirect status=%d err=%v", response.GetStatusCode(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, err = s.Handle(ctx, collectionRequestFor(http.MethodGet, "/collections/catalog", "admin", "", nil))
	if err != nil || response.GetStatusCode() != http.StatusBadGateway {
		t.Fatalf("cancel status=%d err=%v", response.GetStatusCode(), err)
	}
}

func TestCollectionConfigureIsOptionalAndPrivate(t *testing.T) {
	p := newProvider()
	s := &server{p: p, settingsCSRF: "csrf-token"}
	settingsValue, _ := structpb.NewStruct(map[string]any{"api_token": "mediux-token"})
	connectionValue, _ := structpb.NewStruct(map[string]any{"api_key": "collection-secret"})
	if _, err := s.Configure(context.Background(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: settingsValue}, {Key: "collection_connection", Value: connectionValue}}}); err != nil {
		t.Fatal(err)
	}
	config, err := s.settingsConfig()
	if err != nil || config["collection_connection_configured"] != true || strings.Contains(stringMustJSON(config), "collection-secret") {
		t.Fatalf("config=%v err=%v", config, err)
	}
	if _, err := s.Configure(context.Background(), &pluginv1.ConfigureRequest{Config: []*pluginv1.ConfigEntry{{Key: "settings", Value: settingsValue}}}); err != nil {
		t.Fatal(err)
	}
	config, _ = s.settingsConfig()
	if config["collection_connection_configured"] != false {
		t.Fatalf("optional connection not cleared: %v", config)
	}
}

func stringMustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }

func TestCollectionStylesIncludeSharedFontAndControls(t *testing.T) {
	s := collectionTestServer("http://127.0.0.1:8080", "key")
	response, err := s.Handle(context.Background(), collectionRequestFor(http.MethodGet, "/ui.css", "admin", "", nil))
	if err != nil || response.GetStatusCode() != 200 || !strings.Contains(string(response.Body), "@font-face") || !strings.Contains(string(response.Body), "--settings-accent") {
		t.Fatal("Collection page did not receive shared typography and theme controls")
	}
}

func TestCollectionSnapshotCapturesVersionBeforeArtworkListing(t *testing.T) {
	var changed atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/admin/collections" {
			changed.Store(true)
			w.Write([]byte(`{"items":[{"id":"42","title":"Collection","poster_url":"https://images.example/new.jpg"}]}`))
			return
		}
		if changed.Load() {
			w.Header().Set("ETag", `"after"`)
		} else {
			w.Header().Set("ETag", `"before"`)
		}
		w.Write([]byte(`{"id":"42","title":"Collection","poster_url":""}`))
	}))
	defer upstream.Close()
	s := collectionTestServer(upstream.URL, "key")
	_, etag, err := s.collectionSnapshot(context.Background(), "42")
	if err != nil || etag != `"before"` {
		t.Fatalf("Snapshot must capture version before listing so unseen concurrent edits cannot be overwritten: etag=%s error=%v", etag, err)
	}
}
