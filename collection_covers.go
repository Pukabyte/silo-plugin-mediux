package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

// Collection relations contain the collection's own artwork. Movie-owned assets
// can be returned by Mediux alongside them and are discarded below.
const collectionCoverQuery = `query($query:String!){collections(limit:11,sort:["collection_name"],filter:{collection_name:{_icontains:$query}}){id collection_name collection_sets(limit:21,sort:["popularity_global"]){id set_title popularity_global user_created{username} collection_poster(limit:21){id modified_on width height movie{id}} collection_backdrop(limit:21){id modified_on width height movie{id}}}}}`
const collectionByTMDBQuery = `query($id:ID!){collections_by_id(id:$id){id collection_name posters(limit:61,filter:{collection_set:{id:{_nnull:true}}}){id modified_on width height movie{id} collection_set{id set_title popularity_global user_created{username}}} backdrops(limit:61,filter:{collection_set:{id:{_nnull:true}}}){id modified_on width height movie{id} collection_set{id set_title popularity_global user_created{username}}}}}`
const collectionByMovieQuery = `query($id:ID!){movies_by_id(id:$id){collection_id{id collection_name posters(limit:61,filter:{collection_set:{id:{_nnull:true}}}){id modified_on width height movie{id} collection_set{id set_title popularity_global user_created{username}}} backdrops(limit:61,filter:{collection_set:{id:{_nnull:true}}}){id modified_on width height movie{id} collection_set{id set_title popularity_global user_created{username}}}}}}`
const collectionSetCoverQuery = `query($id:ID!){collection_sets_by_id(id:$id){id set_title popularity_global user_created{username} collection_id{id collection_name} collection_poster(limit:61){id modified_on width height movie{id}} collection_backdrop(limit:61){id modified_on width height movie{id}}}}`

var collectionCoverSetID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

type collectionCover struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	URL          string `json:"url"`
	SetID        string `json:"set_id"`
	SetTitle     string `json:"set_title"`
	CollectionID string `json:"collection_id"`
	Creator      string `json:"creator"`
	Title        string `json:"title"`
	SourceURL    string `json:"source_url"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

type collectionCoverResults struct {
	Items   []collectionCover `json:"items"`
	HasMore bool              `json:"has_more"`
}

type collectionCoverAsset struct {
	ID       string `json:"id"`
	Modified string `json:"modified_on"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	Movie    *struct {
		ID string `json:"id"`
	} `json:"movie"`
}

type collectionCoverSet struct {
	ID         string     `json:"id"`
	Title      string     `json:"set_title"`
	GlobalRank mediuxRank `json:"popularity_global"`
	Creator    struct {
		Username string `json:"username"`
	} `json:"user_created"`
	Posters   []collectionCoverAsset `json:"collection_poster"`
	Backdrops []collectionCoverAsset `json:"collection_backdrop"`
}

type collectionCoverSetAsset struct {
	collectionCoverAsset
	Set collectionCoverSet `json:"collection_set"`
}

type collectionCoverCollection struct {
	ID        string                    `json:"id"`
	Name      string                    `json:"collection_name"`
	Sets      []collectionCoverSet      `json:"collection_sets"`
	Posters   []collectionCoverSetAsset `json:"posters"`
	Backdrops []collectionCoverSetAsset `json:"backdrops"`
}

func (p *provider) collectionRequest(ctx context.Context, query string, variables map[string]string) (json.RawMessage, error) {
	cfg := p.snapshot()
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("Mediux API token is not configured")
	}
	body, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-p.sem }()
	response, err := p.client.Do(req)
	if err != nil {
		return nil, errors.New("Mediux collection lookup unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("Mediux collection lookup unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return nil, errors.New("Invalid Mediux collection response")
	}
	var envelope struct {
		Data   json.RawMessage   `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Data) == 0 || len(envelope.Errors) > 0 {
		return nil, errors.New("Invalid Mediux collection response")
	}
	return envelope.Data, nil
}

func collectionResults(groups []collectionCoverSet, collection collectionCoverCollection, cfg settings) collectionCoverResults {
	out := collectionCoverResults{Items: []collectionCover{}}
	b := bundle{}
	metadata := map[string]collectionCover{}
	for _, group := range groups {
		if !collectionCoverSetID.MatchString(group.ID) {
			continue
		}
		s := set{ID: group.ID, Creator: group.Creator.Username, GlobalRank: group.GlobalRank}
		for _, scope := range []struct {
			kind   string
			assets []collectionCoverAsset
		}{{"poster", group.Posters}, {"backdrop", group.Backdrops}} {
			if len(scope.assets) > 20 {
				out.HasMore = true
			}
			for _, asset := range scope.assets {
				if !assetID.MatchString(asset.ID) || asset.Movie != nil {
					continue
				}
				modified, _ := time.Parse(time.RFC3339Nano, asset.Modified)
				a := artwork{ID: asset.ID, Kind: scope.kind, SetID: group.ID, Creator: s.Creator, Modified: modified}
				s.Art = append(s.Art, a)
				metadata[scope.kind+":"+asset.ID] = collectionCover{ID: asset.ID, Kind: scope.kind, URL: resolve(canonical(a, cfg.Quality), ""), SetID: group.ID, SetTitle: group.Title, CollectionID: collection.ID, Creator: s.Creator, Title: collection.Name, SourceURL: "https://mediux.pro/sets/" + group.ID, Width: asset.Width, Height: asset.Height}
			}
		}
		b.Sets = append(b.Sets, s)
	}
	for _, kind := range []string{"poster", "backdrop"} {
		for _, a := range (&provider{}).selectArt(b, cfg, kind, 0, 0, true) {
			if len(out.Items) == 60 {
				out.HasMore = true
				return out
			}
			out.Items = append(out.Items, metadata[kind+":"+a.ID])
		}
	}
	return out
}

func (p *provider) collectionCovers(ctx context.Context, query string) (collectionCoverResults, error) {
	out := collectionCoverResults{Items: []collectionCover{}}
	raw, err := p.collectionRequest(ctx, collectionCoverQuery, map[string]string{"query": query})
	if err != nil {
		return out, err
	}
	var data struct {
		Collections []collectionCoverCollection `json:"collections"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return out, errors.New("Invalid Mediux collection response")
	}
	merged := collectionCoverResults{Items: []collectionCover{}, HasMore: len(data.Collections) > 10}
	for _, collection := range data.Collections {
		part := collectionResults(collection.Sets, collection, p.snapshot())
		merged.HasMore = merged.HasMore || part.HasMore || len(collection.Sets) > 20
		for _, item := range part.Items {
			if len(merged.Items) == 60 {
				merged.HasMore = true
				return merged, nil
			}
			merged.Items = append(merged.Items, item)
		}
	}
	return merged, nil
}

func groupedCollectionAssets(collection collectionCoverCollection, cfg settings) collectionCoverResults {
	byID := map[string]*collectionCoverSet{}
	for _, scope := range []struct {
		kind   string
		assets []collectionCoverSetAsset
	}{{"poster", collection.Posters}, {"backdrop", collection.Backdrops}} {
		for _, asset := range scope.assets {
			if !collectionCoverSetID.MatchString(asset.Set.ID) {
				continue
			}
			group := byID[asset.Set.ID]
			if group == nil {
				copy := asset.Set
				copy.Posters = nil
				copy.Backdrops = nil
				group = &copy
				byID[group.ID] = group
			}
			if scope.kind == "poster" {
				group.Posters = append(group.Posters, asset.collectionCoverAsset)
			} else {
				group.Backdrops = append(group.Backdrops, asset.collectionCoverAsset)
			}
		}
	}
	groups := make([]collectionCoverSet, 0, len(byID))
	for _, group := range byID {
		groups = append(groups, *group)
	}
	return collectionResults(groups, collection, cfg)
}

func (p *provider) collectionTMDBCovers(ctx context.Context, id string) (collectionCoverResults, error) {
	out := collectionCoverResults{Items: []collectionCover{}}
	raw, err := p.collectionRequest(ctx, collectionByTMDBQuery, map[string]string{"id": id})
	if err != nil {
		return out, err
	}
	var data struct {
		Collection *collectionCoverCollection `json:"collections_by_id"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return out, errors.New("Invalid Mediux collection response")
	}
	if data.Collection == nil {
		return out, nil
	}
	if data.Collection.ID != id {
		return out, errors.New("Mediux collection identity mismatch")
	}
	return groupedCollectionAssets(*data.Collection, p.snapshot()), nil
}

func (p *provider) collectionMovieCovers(ctx context.Context, id string) (collectionCoverResults, error) {
	out := collectionCoverResults{Items: []collectionCover{}}
	raw, err := p.collectionRequest(ctx, collectionByMovieQuery, map[string]string{"id": id})
	if err != nil {
		return out, err
	}
	var data struct {
		Movie *struct {
			Collection *collectionCoverCollection `json:"collection_id"`
		} `json:"movies_by_id"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return out, errors.New("Invalid Mediux collection response")
	}
	if data.Movie == nil || data.Movie.Collection == nil {
		return out, nil
	}
	return groupedCollectionAssets(*data.Movie.Collection, p.snapshot()), nil
}

func (p *provider) collectionSetCovers(ctx context.Context, setID string) (collectionCoverResults, error) {
	out := collectionCoverResults{Items: []collectionCover{}}
	raw, err := p.collectionRequest(ctx, collectionSetCoverQuery, map[string]string{"id": setID})
	if err != nil {
		return out, err
	}
	var data struct {
		Set *struct {
			collectionCoverSet
			Collection *collectionCoverCollection `json:"collection_id"`
		} `json:"collection_sets_by_id"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return out, errors.New("Invalid Mediux collection response")
	}
	if data.Set == nil {
		return out, nil
	}
	if data.Set.ID != setID {
		return out, errors.New("Mediux set identity mismatch")
	}
	collection := collectionCoverCollection{}
	if data.Set.Collection != nil {
		collection = *data.Set.Collection
	}
	return collectionResults([]collectionCoverSet{data.Set.collectionCoverSet}, collection, p.snapshot()), nil
}

func parseCollectionSetID(value string) (string, bool) {
	if collectionCoverSetID.MatchString(value) {
		return value, true
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host != "mediux.pro" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "sets" || !collectionCoverSetID.MatchString(parts[1]) {
		return "", false
	}
	return parts[1], true
}

func (s *server) handleCollectionCovers(ctx context.Context, request *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	if request.GetMethod() != "GET" {
		return settingsError(405, "Method not allowed")
	}
	query := request.GetQuery().GetFields()
	values := map[string]string{}
	for _, key := range []string{"q", "tmdb", "movie", "set"} {
		values[key] = strings.TrimSpace(query[key].GetStringValue())
	}
	count := 0
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	if count != 1 {
		return settingsError(400, "Provide exactly one collection selector")
	}
	var out collectionCoverResults
	var err error
	switch {
	case values["q"] != "":
		if len([]rune(values["q"])) < 2 || len([]rune(values["q"])) > 100 || strings.ContainsAny(values["q"], "\r\n\x00") {
			return settingsError(400, "Search must contain 2 to 100 characters")
		}
		out, err = s.p.collectionCovers(ctx, values["q"])
	case values["tmdb"] != "":
		if !collectionCoverSetID.MatchString(values["tmdb"]) {
			return settingsError(400, "Collection TMDB ID is invalid")
		}
		out, err = s.p.collectionTMDBCovers(ctx, values["tmdb"])
	case values["movie"] != "":
		if !collectionCoverSetID.MatchString(values["movie"]) {
			return settingsError(400, "Movie TMDB ID is invalid")
		}
		out, err = s.p.collectionMovieCovers(ctx, values["movie"])
	default:
		setID, ok := parseCollectionSetID(values["set"])
		if !ok {
			return settingsError(400, "Mediux set ID or URL is invalid")
		}
		out, err = s.p.collectionSetCovers(ctx, setID)
	}
	if err != nil {
		return settingsError(502, "Collection artwork lookup failed. Check Mediux access or try again.")
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return settingsResponse(200, body, "application/json"), nil
}
