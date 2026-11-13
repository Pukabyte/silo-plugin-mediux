package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// Extract only the public franchise identifier; never forward source credentials.
func (c *collectionRecord) UnmarshalJSON(raw []byte) error {
	type plain collectionRecord
	var value struct {
		*plain
		Source struct {
			Mode         string          `json:"mode"`
			CollectionID json.RawMessage `json:"collection_id"`
		} `json:"source_config"`
	}
	value.plain = (*plain)(c)
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if value.Source.Mode == "tmdb_collection" {
		id := string(value.Source.CollectionID)
		if len(id) > 1 && id[0] == '"' {
			if err := json.Unmarshal(value.Source.CollectionID, &id); err != nil {
				return err
			}
		}
		if numericID(id) && len(id) <= 20 {
			c.TMDBCollectionID = id
		}
	}
	return nil
}

// Discovery reads the collection's exact TMDB identity first. For other
// collections it offers related franchises from a bounded sample of members.
func (s *server) handleCollectionDiscovery(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	if r.GetHeaders()["X-Silo-User-Role"] != "admin" {
		return settingsError(403, "Administrator access required")
	}
	if r.GetMethod() != http.MethodGet {
		return settingsError(405, "Method not allowed")
	}
	id := r.GetQuery().GetFields()["id"].GetStringValue()
	if !validCollectionID(id) {
		return settingsError(400, "Invalid collection ID")
	}
	collection, _, err := s.collectionSnapshot(ctx, id)
	if err != nil {
		return collectionHTTPError(err)
	}
	var out collectionCoverResults
	basis := "collection_tmdb"
	if collection.TMDBCollectionID != "" {
		// Go through the same validated provider handler used by manual exact lookup.
		q := collectionRequestForDiscovery("tmdb", collection.TMDBCollectionID)
		response, e := s.handleCollectionCovers(ctx, q)
		if e != nil || response.GetStatusCode() != 200 {
			return response, e
		}
		if json.Unmarshal(response.Body, &out) != nil {
			return settingsError(502, "Invalid collection artwork response")
		}
	} else {
		basis = "member_tmdb"
		out = collectionCoverResults{Items: []collectionCover{}}
		if len(collection.LibraryIDs) == 0 || !validCollectionID(collection.LibraryIDs[0]) {
			return settingsError(400, "Collection has no usable library")
		}
		var raw []byte
		if collection.CollectionType == "manual" {
			raw, _, _, err = s.collectionRequest(ctx, http.MethodGet, "/api/v2/admin/collections/"+id+"/items?limit=8", nil, "")
		} else {
			raw, _, _, err = s.catalogRead(ctx, "/api/v2/library/"+collection.LibraryIDs[0]+"/collections/"+id+"/items?limit=8")
		}
		if err != nil {
			return collectionHTTPError(err)
		}
		var page struct {
			Items []struct {
				ContentID string `json:"content_id"`
				MemberID  string `json:"media_item_id"`
				Type      string `json:"type"`
			} `json:"items"`
			Page struct {
				HasMore bool `json:"has_more"`
			} `json:"page"`
		}
		if json.Unmarshal(raw, &page) != nil {
			return settingsError(502, "Invalid member response")
		}
		out.HasMore = page.Page.HasMore || len(page.Items) > 4
		ids := []string{}
		seen := map[string]bool{}
		for i, member := range page.Items {
			if i >= 4 {
				break
			}
			if member.ContentID == "" {
				member.ContentID = member.MemberID
			}
			if (member.Type != "" && member.Type != "movie") || !validCollectionID(member.ContentID) {
				continue
			}
			raw, _, _, e := s.catalogRead(ctx, "/api/v2/catalog/items/"+member.ContentID)
			if e != nil {
				return collectionHTTPError(e)
			}
			var detail struct {
				TMDB string `json:"tmdb_id"`
				Type string `json:"type"`
			}
			if json.Unmarshal(raw, &detail) != nil {
				return settingsError(502, "Invalid member identity")
			}
			if (detail.Type == "" || detail.Type == "movie") && numericID(detail.TMDB) && len(detail.TMDB) <= 20 && !seen[detail.TMDB] {
				seen[detail.TMDB] = true
				ids = append(ids, detail.TMDB)
			}
		}
		results := make([]collectionCoverResults, len(ids))
		failed := make([]bool, len(ids))
		var workers sync.WaitGroup
		for index, tmdb := range ids {
			workers.Add(1)
			go func(index int, tmdb string) {
				defer workers.Done()
				response, e := s.handleCollectionCovers(ctx, collectionRequestForDiscovery("movie", tmdb))
				if e != nil || response.GetStatusCode() != 200 || json.Unmarshal(response.Body, &results[index]) != nil {
					failed[index] = true
				}
			}(index, tmdb)
		}
		workers.Wait()
		assets := map[string]bool{}
		for i, result := range results {
			if failed[i] {
				return settingsError(502, "Related collection lookup failed. Try a franchise name or exact TMDB collection ID.")
			}
			out.HasMore = out.HasMore || result.HasMore
			for _, asset := range result.Items {
				key := asset.Kind + ":" + asset.ID
				if !assets[key] && len(out.Items) < 60 {
					assets[key] = true
					out.Items = append(out.Items, asset)
				} else if !assets[key] {
					out.HasMore = true
				}
			}
		}
	}
	body, e := json.Marshal(map[string]any{"items": out.Items, "has_more": out.HasMore, "basis": basis})
	if e != nil {
		return nil, e
	}
	return settingsResponse(200, body, "application/json"), nil
}

func collectionRequestForDiscovery(key, value string) *pluginv1.HandleHTTPRequest {
	q, _ := structpb.NewStruct(map[string]any{key: value})
	return &pluginv1.HandleHTTPRequest{Method: http.MethodGet, Path: "/collection-artwork", Headers: map[string]string{"X-Silo-User-Role": "admin"}, Query: q}
}
