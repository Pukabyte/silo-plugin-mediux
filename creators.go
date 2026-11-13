package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

const creatorQuery = `query($query:String!){contributors(limit:51,sort:["username"],filter:{username:{_icontains:$query}}){username}}`

type creatorOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type creatorResults struct {
	Items   []creatorOption `json:"items"`
	HasMore bool            `json:"has_more"`
}

func (p *provider) creators(ctx context.Context, query string) (creatorResults, error) {
	out := creatorResults{Items: []creatorOption{}}
	cfg := p.snapshot()
	if strings.TrimSpace(cfg.Token) == "" {
		return out, errors.New("Mediux API token is not configured")
	}
	body, _ := json.Marshal(map[string]any{"query": creatorQuery, "variables": map[string]string{"query": query}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return out, ctx.Err()
	}
	defer func() { <-p.sem }()
	res, err := p.client.Do(req)
	if err != nil {
		return out, errors.New("Mediux creator lookup unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return out, errors.New("Mediux creator lookup unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return out, errors.New("Invalid Mediux creator response")
	}
	var data struct {
		Data struct {
			Contributors []struct {
				Username string `json:"username"`
			} `json:"contributors"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &data); err != nil || len(data.Errors) > 0 {
		return out, errors.New("Invalid Mediux creator response")
	}
	seen := map[string]bool{}
	for _, c := range data.Data.Contributors {
		name := strings.TrimSpace(c.Username)
		key := strings.ToLower(name)
		if name == "" || len(name) > 128 || strings.ContainsAny(name, ",\r\n") || seen[key] {
			continue
		}
		seen[key] = true
		if len(out.Items) == 50 {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, creatorOption{Value: name, Label: name})
	}
	return out, nil
}

// Handle exposes only creator names; authentication and the saved token stay
// behind Silo's admin-only plugin route.
func (s *server) Handle(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	respond := func(status int, data any) (*pluginv1.HandleHTTPResponse, error) {
		body, err := json.Marshal(data)
		return &pluginv1.HandleHTTPResponse{StatusCode: int32(status), Body: body, Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}}, err
	}
	if r.GetHeaders()["X-Silo-User-Role"] != "admin" {
		return respond(403, map[string]string{"error": "Administrator access required"})
	}
	if r.GetPath() == "/artwork" {
		return s.handleArtworkManager(ctx, r)
	}
	if r.GetPath() == "/sets/plan" || r.GetPath() == "/sets/apply" {
		return s.handleSetApply(ctx, r)
	}
	if r.GetPath() == "/sets/items" || r.GetPath() == "/sets/item" {
		return s.handleSetLibrary(ctx, r)
	}
	if r.GetPath() == "/sets" || r.GetPath() == "/sets/catalog" || r.GetPath() == "/sets/preferences" {
		return s.handleSets(ctx, r)
	}
	if r.GetPath() == "/collections/discover" {
		return s.handleCollectionDiscovery(ctx, r)
	}
	if r.GetPath() == "/collection-artwork" {
		return s.handleCollectionCovers(ctx, r)
	}
	if r.GetPath() == "/collections" || r.GetPath() == "/collections/catalog" || r.GetPath() == "/collections/item" || r.GetPath() == "/collections/cover" || r.GetPath() == "/ui.css" {
		return s.handleCollections(ctx, r)
	}
	if r.GetPath() == "/settings" || r.GetPath() == "/settings/config" {
		return s.handleSettings(ctx, r)
	}
	if r.GetMethod() != "GET" || r.GetPath() != "/creators" {
		return respond(404, map[string]string{"error": "Not found"})
	}
	query := strings.TrimSpace(r.GetQuery().GetFields()["q"].GetStringValue())
	if len([]rune(query)) < 2 || len([]rune(query)) > 80 {
		return respond(400, map[string]string{"error": "Search must contain 2 to 80 characters"})
	}
	out, err := s.p.creators(ctx, query)
	if err != nil {
		return respond(502, map[string]string{"error": "Creator lookup failed. Check the saved Mediux token or try again."})
	}
	return respond(200, out)
}
