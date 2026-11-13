package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

//go:embed collections.html
var collectionsPage []byte

const (
	maxCollectionBody = 16 << 10
	maxNativeBody     = 4 << 20
)

type collectionRecord struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	PosterURL        string   `json:"poster_url"`
	BackdropURL      string   `json:"backdrop_url"`
	LibraryIDs       []string `json:"library_ids"`
	ItemCount        int      `json:"item_count"`
	TMDBCollectionID string   `json:"tmdb_collection_id,omitempty"`
	CollectionType   string   `json:"collection_type,omitempty"`
}

type collectionCoverPatch struct {
	CSRF string `json:"csrf"`
	ID   string `json:"id"`
	Slot string `json:"slot"`
	URL  string `json:"url"`
	ETag string `json:"etag"`
}

type collectionFailure struct {
	status  int
	message string
}

func (e *collectionFailure) Error() string { return e.message }

func collectionError(status int, message string) error {
	return &collectionFailure{status: status, message: message}
}

func collectionConnectionMessage() string {
	return "Collection connection is not configured. Add the dedicated Silo admin API key in Mediux plugin settings."
}

func validCollectionID(id string) bool {
	if id == "" || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func hasControl(value string) bool {
	for _, r := range value {
		if r <= 0x1f || r == 0x7f {
			return true
		}
	}
	return false
}

func validRemoteArtworkURL(value string) bool {
	if value == "" || len(value) > 4096 || hasControl(value) || !utf8.ValidString(value) {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	return true
}

func validETag(value string) bool {
	if len(value) < 2 || len(value) > 1024 || hasControl(value) || strings.Contains(value, "*") {
		return false
	}
	return value[0] == '"' && value[len(value)-1] == '"'
}

func validatedCollectionBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid collection host")
	}
	return u, nil
}

func collectionClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (s *server) collectionKeyAndCSRF() (string, string, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	csrf, err := s.settingsCSRFLocked()
	if err != nil {
		return "", "", err
	}
	return s.collectionKey, csrf, nil
}

func (s *server) collectionKeyValue() string {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.collectionKey
}

func (s *server) collectionRequest(ctx context.Context, method, endpoint string, body []byte, etag string) ([]byte, http.Header, int, error) {
	return s.collectionRequestWithProfile(ctx, method, endpoint, body, etag, "")
}

func (s *server) collectionRequestWithProfile(ctx context.Context, method, endpoint string, body []byte, etag, profile string) ([]byte, http.Header, int, error) {
	key := s.collectionKeyValue()
	if strings.TrimSpace(key) == "" {
		return nil, nil, 0, collectionError(http.StatusServiceUnavailable, collectionConnectionMessage())
	}
	if s.collectionBaseURL == nil {
		return nil, nil, 0, collectionError(http.StatusBadGateway, "Collection service is unavailable")
	}
	rawBase, err := s.collectionBaseURL(ctx)
	if err != nil {
		return nil, nil, 0, collectionError(http.StatusBadGateway, "Collection service is unavailable")
	}
	base, err := validatedCollectionBaseURL(rawBase)
	if err != nil {
		return nil, nil, 0, collectionError(http.StatusBadGateway, "Collection service is unavailable")
	}
	relative, err := url.Parse(endpoint)
	if err != nil || relative.IsAbs() || relative.Host != "" || !strings.HasPrefix(relative.Path, "/api/v2/") || relative.Fragment != "" {
		return nil, nil, 0, collectionError(http.StatusBadGateway, "Invalid collection endpoint")
	}
	base.Path = strings.TrimRight(base.Path, "/") + relative.Path
	base.RawPath = ""
	base.RawQuery = relative.RawQuery
	req, err := http.NewRequestWithContext(ctx, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, collectionError(http.StatusBadGateway, "Collection service is unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if profile != "" {
		req.Header.Set("X-Profile-Id", profile)
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	client := collectionClient()
	if method == http.MethodPost && strings.HasSuffix(relative.Path, "/images/apply") {
		client.Timeout = 45 * time.Second
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, nil, 0, collectionError(http.StatusBadGateway, "Collection service is unavailable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxNativeBody+1))
	if err != nil || len(raw) > maxNativeBody {
		return nil, nil, 0, collectionError(http.StatusBadGateway, "Invalid collection service response")
	}
	if res.StatusCode == http.StatusPreconditionFailed {
		return nil, nil, res.StatusCode, collectionError(http.StatusPreconditionFailed, "Collection changed. Reload it before applying artwork.")
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, nil, res.StatusCode, collectionError(http.StatusNotFound, "Collection no longer exists")
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return nil, nil, res.StatusCode, collectionError(http.StatusServiceUnavailable, "Collection connection was refused. Update the plugin's collection connection.")
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, nil, res.StatusCode, collectionError(http.StatusBadGateway, "Collection service request failed")
	}
	return raw, res.Header, res.StatusCode, nil
}

func (s *server) collectionCatalog(ctx context.Context) ([]collectionRecord, error) {
	raw, _, _, err := s.collectionRequest(ctx, http.MethodGet, "/api/v2/admin/collections", nil, "")
	if err != nil {
		return nil, err
	}
	var response struct {
		Items []collectionRecord `json:"items"`
	}
	if err := json.Unmarshal(raw, &response); err != nil || response.Items == nil {
		return nil, collectionError(http.StatusBadGateway, "Invalid collection service response")
	}
	return response.Items, nil
}

func (s *server) collectionSnapshot(ctx context.Context, id string) (collectionRecord, string, error) {
	raw, headers, _, err := s.collectionRequest(ctx, http.MethodGet, "/api/v2/admin/collections/"+id, nil, "")
	if err != nil {
		return collectionRecord{}, "", err
	}
	var detail collectionRecord
	if err := json.Unmarshal(raw, &detail); err != nil || detail.ID != id {
		return collectionRecord{}, "", collectionError(http.StatusBadGateway, "Invalid collection service response")
	}
	etag := headers.Get("ETag")
	if !validETag(etag) {
		return collectionRecord{}, "", collectionError(http.StatusBadGateway, "Collection service did not provide a valid version")
	}
	items, err := s.collectionCatalog(ctx)
	if err != nil {
		return collectionRecord{}, "", err
	}
	var listed collectionRecord
	found := false
	for _, item := range items {
		if item.ID == id {
			listed, found = item, true
			break
		}
	}
	if !found {
		return collectionRecord{}, "", collectionError(http.StatusNotFound, "Collection no longer exists")
	}

	detail.PosterURL = listed.PosterURL
	detail.BackdropURL = listed.BackdropURL
	return detail, etag, nil
}

func decodeCollectionCoverPatch(body []byte) (collectionCoverPatch, error) {
	if len(body) > maxCollectionBody {
		return collectionCoverPatch{}, collectionError(http.StatusRequestEntityTooLarge, "Collection artwork payload is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var patch collectionCoverPatch
	if err := decoder.Decode(&patch); err != nil {
		return collectionCoverPatch{}, collectionError(http.StatusBadRequest, "Collection artwork payload must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return collectionCoverPatch{}, collectionError(http.StatusBadRequest, "Collection artwork payload must contain one JSON object")
	}
	if !validCollectionID(patch.ID) || !validRemoteArtworkURL(patch.URL) || !validETag(patch.ETag) || (patch.Slot != "poster" && patch.Slot != "backdrop") {
		return collectionCoverPatch{}, collectionError(http.StatusBadRequest, "Invalid collection artwork request")
	}
	return patch, nil
}

func collectionHTTPError(err error) (*pluginv1.HandleHTTPResponse, error) {
	if typed, ok := err.(*collectionFailure); ok {
		return settingsError(typed.status, typed.message)
	}
	return settingsError(http.StatusInternalServerError, "Could not process collection artwork")
}

func (s *server) handleCollectionCover(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	patch, err := decodeCollectionCoverPatch(r.GetBody())
	if err != nil {
		return collectionHTTPError(err)
	}
	key, csrf, err := s.collectionKeyAndCSRF()
	if err != nil {
		return settingsError(http.StatusInternalServerError, "Could not prepare collection artwork")
	}
	if subtle.ConstantTimeCompare([]byte(patch.CSRF), []byte(csrf)) != 1 {
		return settingsError(http.StatusForbidden, "Invalid CSRF token")
	}
	if strings.TrimSpace(key) == "" {
		return settingsError(http.StatusServiceUnavailable, collectionConnectionMessage())
	}
	field := "poster_url"
	if patch.Slot == "backdrop" {
		field = "backdrop_url"
	}
	body, _ := json.Marshal(map[string]string{field: patch.URL})
	if _, _, _, err := s.collectionRequest(ctx, http.MethodPatch, "/api/v2/admin/collections/"+patch.ID, body, patch.ETag); err != nil {
		return collectionHTTPError(err)
	}
	collection, nextETag, err := s.collectionSnapshot(ctx, patch.ID)
	if err != nil {
		return collectionHTTPError(err)
	}
	actual := collection.PosterURL
	if patch.Slot == "backdrop" {
		actual = collection.BackdropURL
	}
	if actual != patch.URL {
		return settingsError(http.StatusBadGateway, "Collection service did not apply the selected artwork")
	}
	response, err := json.Marshal(map[string]any{"collection": collection, "etag": nextETag})
	if err != nil {
		return nil, err
	}
	return settingsResponse(http.StatusOK, response, "application/json"), nil
}

func (s *server) handleCollections(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	switch {
	case r.GetMethod() == http.MethodGet && r.GetPath() == "/collections":
		return s.handleArtworkManager(ctx, r)
	case r.GetMethod() == http.MethodGet && r.GetPath() == "/ui.css":
		start := bytes.Index(settingsPage, []byte("<style>"))
		end := bytes.Index(settingsPage, []byte("</style>"))
		if start < 0 || end <= start {
			return settingsError(http.StatusInternalServerError, "Shared plugin styles are unavailable")
		}
		return settingsResponse(http.StatusOK, settingsPage[start+len("<style>"):end], "text/css; charset=utf-8"), nil
	case r.GetMethod() == http.MethodGet && r.GetPath() == "/collections/catalog":
		items, err := s.collectionCatalog(ctx)
		if err != nil {
			return collectionHTTPError(err)
		}
		body, err := json.Marshal(map[string]any{"items": items, "connected": true})
		if err != nil {
			return nil, err
		}
		return settingsResponse(http.StatusOK, body, "application/json"), nil
	case r.GetMethod() == http.MethodGet && r.GetPath() == "/collections/item":
		id := r.GetQuery().GetFields()["id"].GetStringValue()
		if !validCollectionID(id) {
			return settingsError(http.StatusBadRequest, "Invalid collection ID")
		}
		collection, etag, err := s.collectionSnapshot(ctx, id)
		if err != nil {
			return collectionHTTPError(err)
		}
		body, err := json.Marshal(map[string]any{"collection": collection, "etag": etag})
		if err != nil {
			return nil, err
		}
		return settingsResponse(http.StatusOK, body, "application/json"), nil
	case r.GetMethod() == http.MethodPost && r.GetPath() == "/collections/cover":
		return s.handleCollectionCover(ctx, r)
	default:
		return settingsError(http.StatusNotFound, "Not found")
	}
}
