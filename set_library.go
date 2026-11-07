package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

type setLibraryItem struct {
	ContentID string `json:"content_id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
}

func (s *server) handleSetLibrary(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	if r.GetHeaders()["X-Silo-User-Role"] != "admin" {
		return settingsError(403, "Administrator access required")
	}
	if r.GetMethod() != http.MethodGet {
		return settingsError(405, "Method not allowed")
	}
	fields := r.GetQuery().GetFields()
	if r.GetPath() == "/sets/items" {
		typ := fields["type"].GetStringValue()
		q := strings.TrimSpace(fields["q"].GetStringValue())
		if (typ != "movie" && typ != "series") || len([]rune(q)) < 2 || len([]rune(q)) > 100 || hasControl(q) {
			return settingsError(400, "Choose movies or shows and enter 2 to 100 search characters")
		}
		query := url.Values{"type": {typ}, "q": {q}, "limit": {"20"}, "skip_total": {"true"}}
		raw, _, _, err := s.catalogRead(ctx, "/api/v2/catalog?"+query.Encode())
		if err != nil {
			return collectionHTTPError(err)
		}
		var data struct {
			Items []setLibraryItem `json:"items"`
			Page  struct {
				HasMore bool `json:"has_more"`
			} `json:"page"`
		}
		if json.Unmarshal(raw, &data) != nil {
			return settingsError(502, "Invalid library search response")
		}
		out := []setLibraryItem{}
		for _, item := range data.Items {
			if len(out) == 20 {
				break
			}
			if item.Type == typ && validCollectionID(item.ContentID) {
				out = append(out, item)
			}
		}
		body, _ := json.Marshal(map[string]any{"items": out, "has_more": data.Page.HasMore})
		return settingsResponse(200, body, "application/json"), nil
	}
	id := fields["id"].GetStringValue()
	if !validCollectionID(id) {
		return settingsError(400, "Invalid library item")
	}
	raw, _, _, err := s.catalogRead(ctx, "/api/v2/catalog/items/"+id)
	if err != nil {
		return collectionHTTPError(err)
	}
	var item struct {
		Type  string `json:"type"`
		Title string `json:"title"`
		TMDB  string `json:"tmdb_id"`
		TVDB  string `json:"tvdb_id"`
	}
	if json.Unmarshal(raw, &item) != nil || (item.Type != "movie" && item.Type != "series") {
		return settingsError(400, "Only movies and shows support set preferences")
	}
	if !numericID(item.TMDB) && numericID(item.TVDB) {
		item.TMDB, err = s.p.resolveTVDB(ctx, item.Type, item.TVDB)
		if err != nil {
			return settingsError(502, "Could not identify this title in Mediux")
		}
	}
	if !numericID(item.TMDB) || len(item.TMDB) > 20 {
		return settingsError(422, "This title needs a TMDB or TVDB identity before choosing Mediux sets")
	}
	body, _ := json.Marshal(map[string]any{"type": item.Type, "id": item.TMDB, "title": item.Title})
	return settingsResponse(200, body, "application/json"), nil
}

// Catalog APIs require an explicit household profile. Select only the dedicated
// key account's primary adult profile without a PIN, using the documented list
// and header. No browser session, profile PIN or other user's profile is used.
func (s *server) catalogRead(ctx context.Context, endpoint string) ([]byte, http.Header, int, error) {
	raw, _, _, err := s.collectionRequest(ctx, http.MethodGet, "/api/v2/profiles", nil, "")
	if err != nil {
		return nil, nil, 0, err
	}
	var data struct {
		Items []struct {
			ID      string `json:"id"`
			Primary bool   `json:"is_primary"`
			Child   bool   `json:"is_child"`
			PIN     bool   `json:"has_pin"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return nil, nil, 0, collectionError(502, "Invalid catalog profile response")
	}
	for _, profile := range data.Items {
		if profile.Primary && !profile.Child && !profile.PIN && validCollectionID(profile.ID) {
			return s.collectionRequestWithProfile(ctx, http.MethodGet, endpoint, nil, "", profile.ID)
		}
	}
	return nil, nil, 0, collectionError(503, "The dedicated Silo key account needs a primary adult profile without a PIN for library browsing. TMDB set lookup remains available.")
}
