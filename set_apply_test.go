package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const setApplyPosterID = "11111111-1111-4111-8111-111111111111"
const setApplySeasonID = "22222222-2222-4222-8222-222222222222"
const setApplyStillID = "33333333-3333-4333-8333-333333333333"

func setApplyTestServer(baseURL string, art []artwork) *server {
	s := collectionTestServer(baseURL, "set-apply-key")
	s.p.cache["0:series:100"] = cacheEntry{b: bundle{Sets: []set{{ID: "9", Creator: "Tester", Art: art}}}, until: time.Now().Add(time.Hour)}
	s.p.cache["0:movie:100"] = cacheEntry{b: bundle{Sets: []set{{ID: "9", Creator: "Tester", Art: art}}}, until: time.Now().Add(time.Hour)}
	return s
}

func TestSetApplyPlanIsReadOnlyAndMapsNativeChildren(t *testing.T) {
	var writes atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writes.Add(1)
		}
		switch r.URL.Path {
		case "/api/v2/profiles":
			_, _ = w.Write([]byte(`{"items":[{"id":"primary","is_primary":true,"is_child":false,"has_pin":false}]}`))
		case "/api/v2/catalog/items/series:parent":
			_, _ = w.Write([]byte(`{"content_id":"series:parent","type":"series","tmdb_id":"100"}`))
		case "/api/v2/catalog/series/series:parent/seasons":
			_, _ = w.Write([]byte(`{"items":[{"content_id":"season:one","season_number":1}]}`))
		case "/api/v2/catalog/series/series:parent/seasons/1/episodes":
			_, _ = w.Write([]byte(`{"items":[{"content_id":"episode:one","season_number":1,"episode_number":2}]}`))
		default:
			t.Errorf("unexpected native call %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer native.Close()
	s := setApplyTestServer(native.URL, []artwork{
		{ID: setApplyPosterID, Kind: "poster", SetID: "9", Language: "en"},
		{ID: setApplySeasonID, Kind: "season", Season: 1, SetID: "9", Language: "en"},
		{ID: setApplyStillID, Kind: "still", Season: 1, Episode: 2, SetID: "9", Language: "en"},
	})
	body := `{"csrf":"csrf-token","type":"series","id":"100","content_id":"series:parent","set_id":"9"}`
	res, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/plan", "admin", body, nil))
	if err != nil || res.GetStatusCode() != http.StatusOK || writes.Load() != 0 {
		t.Fatalf("plan status=%d writes=%d err=%v body=%s", res.GetStatusCode(), writes.Load(), err, res.GetBody())
	}
	var plan struct {
		Operations []setApplyOperation `json:"operations"`
	}
	if json.Unmarshal(res.GetBody(), &plan) != nil || len(plan.Operations) != 3 {
		t.Fatalf("unexpected plan: %s", res.GetBody())
	}
	targets := []string{}
	for _, operation := range plan.Operations {
		if operation.Token == "" || !strings.HasPrefix(operation.URL, "https://images.mediux.io/assets/") {
			t.Fatalf("unsafe operation: %#v", operation)
		}
		targets = append(targets, operation.TargetID)
	}
	sort.Strings(targets)
	if got, want := strings.Join(targets, ","), "episode:one,season:one,series:parent"; got != want {
		t.Fatalf("child mapping=%q want=%q", got, want)
	}
}

func TestSetApplyRejectsCSRFBeforeAnyUpstreamCall(t *testing.T) {
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer native.Close()
	s := setApplyTestServer(native.URL, nil)
	res, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/plan", "admin", `{"csrf":"wrong","type":"movie","id":"100","set_id":"9"}`, nil))
	if err != nil || res.GetStatusCode() != http.StatusForbidden || calls.Load() != 0 {
		t.Fatalf("plan status=%d calls=%d err=%v", res.GetStatusCode(), calls.Load(), err)
	}
	res, err = s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/apply", "admin", `{"csrf":"wrong","token":"anything"}`, nil))
	if err != nil || res.GetStatusCode() != http.StatusForbidden || calls.Load() != 0 {
		t.Fatalf("apply status=%d calls=%d err=%v", res.GetStatusCode(), calls.Load(), err)
	}
}

func TestSetApplyPlanRejectsCrossIdentityContentID(t *testing.T) {
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/api/v2/profiles":
			_, _ = w.Write([]byte(`{"items":[{"id":"primary","is_primary":true}]}`))
		case "/api/v2/catalog/items/movie-other":
			_, _ = w.Write([]byte(`{"content_id":"movie-other","type":"movie","tmdb_id":"999"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer native.Close()
	s := setApplyTestServer(native.URL, []artwork{{ID: setApplyPosterID, Kind: "poster", SetID: "9"}})
	body := `{"csrf":"csrf-token","type":"movie","id":"100","content_id":"movie-other","set_id":"9"}`
	res, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/plan", "admin", body, nil))
	if err != nil || res.GetStatusCode() != http.StatusBadRequest || calls.Load() != 2 {
		t.Fatalf("status=%d calls=%d err=%v body=%s", res.GetStatusCode(), calls.Load(), err, res.GetBody())
	}
}

func TestSetApplyRejectsForgedTokenWithoutNativeCall(t *testing.T) {
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer native.Close()
	s := setApplyTestServer(native.URL, nil)
	source := resolve(canonical(artwork{ID: setApplyPosterID, Kind: "poster"}, "optimized"), "")
	token, err := s.signSetApplyToken(setApplyToken{TargetID: "movie-native", SourceURL: source, ImageType: "poster", Kind: "poster", Type: "movie", ID: "100", SetID: "9", Role: "admin", AssetID: setApplyPosterID, ExpiresAt: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	forged := token[:len(token)-1] + "x"
	body := `{"csrf":"csrf-token","token":"` + forged + `"}`
	res, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/apply", "admin", body, nil))
	if err != nil || res.GetStatusCode() != http.StatusForbidden || calls.Load() != 0 {
		t.Fatalf("status=%d calls=%d err=%v body=%s", res.GetStatusCode(), calls.Load(), err, res.GetBody())
	}
}

func TestSetApplyFreshPlansUseDistinctSingleUseTokens(t *testing.T) {
	var writes atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/profiles":
			_, _ = w.Write([]byte(`{"items":[{"id":"primary","is_primary":true}]}`))
		case "/api/v2/catalog/items/movie:parent":
			_, _ = w.Write([]byte(`{"content_id":"movie:parent","type":"movie","tmdb_id":"100"}`))
		case "/api/v2/admin/items/movie:parent/images/apply":
			writes.Add(1)
			_, _ = w.Write([]byte(`{"revision":"applied"}`))
		default:
			t.Errorf("unexpected native call %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer native.Close()
	s := setApplyTestServer(native.URL, []artwork{{ID: setApplyPosterID, Kind: "poster", SetID: "9", Language: "en"}})
	planBody := `{"csrf":"csrf-token","type":"movie","id":"100","content_id":"movie:parent","set_id":"9","asset_id":"` + setApplyPosterID + `","kind":"poster"}`
	planToken := func() string {
		t.Helper()
		res, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/plan", "admin", planBody, nil))
		if err != nil || res.GetStatusCode() != http.StatusOK {
			t.Fatalf("plan status=%d err=%v body=%s", res.GetStatusCode(), err, res.GetBody())
		}
		var plan struct {
			Operations []setApplyOperation `json:"operations"`
		}
		if json.Unmarshal(res.GetBody(), &plan) != nil || len(plan.Operations) != 1 {
			t.Fatalf("unexpected plan: %s", res.GetBody())
		}
		return plan.Operations[0].Token
	}
	first, second := planToken(), planToken()
	if first == second {
		t.Fatal("fresh plans reused an operation token")
	}
	for _, token := range []string{first, second} {
		res, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/apply", "admin", `{"csrf":"csrf-token","token":"`+token+`"}`, nil))
		if err != nil || res.GetStatusCode() != http.StatusOK {
			t.Fatalf("apply status=%d err=%v body=%s", res.GetStatusCode(), err, res.GetBody())
		}
	}
	if writes.Load() != 2 {
		t.Fatalf("native writes=%d want=2", writes.Load())
	}
}

func TestSetApplyConcurrentReplayMakesOneNativeCall(t *testing.T) {
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"revision":"applied"}`))
	}))
	defer native.Close()
	s := setApplyTestServer(native.URL, nil)
	token := testSetApplyToken(t, s, "movie-native", "movie", "poster")
	start := make(chan struct{})
	var wg sync.WaitGroup
	var success atomic.Int32
	var conflicts atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/apply", "admin", `{"csrf":"csrf-token","token":"`+token+`"}`, nil))
			if err != nil {
				t.Errorf("apply error: %v", err)
				return
			}
			switch res.GetStatusCode() {
			case http.StatusOK:
				success.Add(1)
			case http.StatusConflict:
				conflicts.Add(1)
			default:
				t.Errorf("unexpected status %d", res.GetStatusCode())
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := calls.Load(); got != 1 || success.Load() != 1 || conflicts.Load() != 7 {
		t.Fatalf("native=%d success=%d conflicts=%d", got, success.Load(), conflicts.Load())
	}
}

func TestSetApplyDoesNotRetryNativeFailure(t *testing.T) {
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/admin/items/movie-native/images/apply" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer native.Close()
	s := setApplyTestServer(native.URL, nil)
	token := testSetApplyToken(t, s, "movie-native", "movie", "poster")
	res, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/apply", "admin", `{"csrf":"csrf-token","token":"`+token+`"}`, nil))
	if err != nil || res.GetStatusCode() != http.StatusBadGateway || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d err=%v body=%s", res.GetStatusCode(), calls.Load(), err, res.GetBody())
	}
	res, err = s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/apply", "admin", `{"csrf":"csrf-token","token":"`+token+`"}`, nil))
	if err != nil || res.GetStatusCode() != http.StatusConflict || calls.Load() != 1 {
		t.Fatalf("replay status=%d calls=%d err=%v body=%s", res.GetStatusCode(), calls.Load(), err, res.GetBody())
	}
}

func TestSetApplyReturnsNativePublicationResult(t *testing.T) {
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["provider_id"] != "mediux" || payload["type"] != "poster" || !strings.HasPrefix(payload["original_url"], "https://images.mediux.io/assets/") {
			t.Errorf("bad native payload: %#v", payload)
		}
		_, _ = w.Write([]byte(`{"revision":"rev-7","image_url":"https://silo/image.jpg","stored_path":"/images/image.jpg"}`))
	}))
	defer native.Close()
	s := setApplyTestServer(native.URL, nil)
	token := testSetApplyToken(t, s, "movie-native", "movie", "poster")
	res, err := s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/apply", "admin", `{"csrf":"csrf-token","token":"`+token+`"}`, nil))
	if err != nil || res.GetStatusCode() != http.StatusOK || calls.Load() != 1 || !strings.Contains(string(res.GetBody()), `"target_id":"movie-native"`) || !strings.Contains(string(res.GetBody()), `"revision":"rev-7"`) {
		t.Fatalf("status=%d calls=%d err=%v body=%s", res.GetStatusCode(), calls.Load(), err, res.GetBody())
	}
	res, err = s.Handle(context.Background(), collectionRequestFor(http.MethodPost, "/sets/apply", "admin", `{"csrf":"csrf-token","token":"`+token+`"}`, nil))
	if err != nil || res.GetStatusCode() != http.StatusConflict || calls.Load() != 1 {
		t.Fatalf("replay status=%d calls=%d err=%v body=%s", res.GetStatusCode(), calls.Load(), err, res.GetBody())
	}
}

func testSetApplyToken(t *testing.T, s *server, target, typ, kind string) string {
	t.Helper()
	asset := setApplyPosterID
	source := resolve(canonical(artwork{ID: asset, Kind: kind}, "optimized"), "")
	token, err := s.signSetApplyToken(setApplyToken{TargetID: target, SourceURL: source, ImageType: nativeImageType(kind), Kind: kind, Type: typ, ID: "100", SetID: "9", Role: "admin", AssetID: asset, ExpiresAt: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestSetApplyRejectsAlternateSignatureEncoding(t *testing.T) {
	s := setApplyTestServer("http://127.0.0.1:1", nil)
	token := testSetApplyToken(t, s, "movie-native", "movie", "poster")
	if _, err := s.verifySetApplyToken(token + "\n"); err == nil {
		t.Fatal("non-canonical signature could bypass one-use token identity")
	}
}
