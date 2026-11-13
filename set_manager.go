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
	"regexp"
	"sort"
	"strconv"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

//go:embed sets.html
var setsPage []byte
var numericSetID = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

type setCatalogArtwork struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	SetID    string `json:"set_id"`
	Season   int    `json:"season,omitempty"`
	Episode  int    `json:"episode,omitempty"`
	Language string `json:"language,omitempty"`
}
type setCatalogSet struct {
	ID           string              `json:"id"`
	Title        string              `json:"title"`
	Creator      string              `json:"creator"`
	GlobalRank   mediuxRank          `json:"popularity_global"`
	Artwork      []setCatalogArtwork `json:"artwork"`
	Counts       map[string]int      `json:"counts"`
	ArtworkCount int                 `json:"artwork_count"`
}
type setCatalogResult struct {
	Sets       []setCatalogSet `json:"sets"`
	HasMore    bool            `json:"has_more,omitempty"`
	NextOffset int             `json:"next_offset,omitempty"`
}

func setBindingKey(typ, id string) string { return typ + ":" + id }
func validSetType(v string) bool          { return v == "movie" || v == "series" || v == "collection" }
func validSetID(v string) bool            { return numericSetID.MatchString(v) }
func (s *server) preferredSet(typ, id string) string {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	return s.setBindings[setBindingKey(typ, id)]
}

func (s *server) setCatalog(ctx context.Context, typ, id string) (setCatalogResult, error) {
	return s.setCatalogPage(ctx, typ, id, 0)
}

func (s *server) setCatalogPage(ctx context.Context, typ, id string, offset int) (setCatalogResult, error) {
	if !validSetType(typ) || !validSetID(id) || typ == "collection" {
		return setCatalogResult{}, errors.New("invalid set target")
	}
	b, err := s.p.get(ctx, typ, id)
	if err != nil {
		return setCatalogResult{}, err
	}
	cfg := s.p.snapshot()
	preferred := s.preferredSet(typ, id)
	groups := append([]set(nil), b.Sets...)
	priorities := splitSet(cfg.Priority)
	sort.SliceStable(groups, func(i, j int) bool {
		a, z := groups[i], groups[j]
		if (a.ID == preferred) != (z.ID == preferred) {
			return a.ID == preferred
		}
		ai, aok := priorities[strings.ToLower(a.Creator)]
		zi, zok := priorities[strings.ToLower(z.Creator)]
		if aok != zok {
			return aok
		}
		if aok && ai != zi {
			return ai < zi
		}
		if ahead, behind := a.GlobalRank.before(z.GlobalRank), z.GlobalRank.before(a.GlobalRank); ahead || behind {
			return ahead
		}
		return a.ID < z.ID
	})
	out := setCatalogResult{Sets: make([]setCatalogSet, 0, len(b.Sets))}
	total := 0
	priority, excluded := splitSet(cfg.Priority), splitSet(cfg.Excluded)
	for _, g := range groups {
		if !validSetID(g.ID) {
			continue
		}
		_, ok := priority[strings.ToLower(g.Creator)]
		if _, bad := excluded[strings.ToLower(g.Creator)]; bad || cfg.OnlyPriority && !ok {
			continue
		}
		item := setCatalogSet{ID: g.ID, Title: g.Title, Creator: g.Creator, GlobalRank: g.GlobalRank, Counts: map[string]int{}}
		if item.Title == "" {
			item.Title = "Mediux set " + g.ID
		}
		for _, a := range g.Art {
			if a.Kind != "poster" && a.Kind != "backdrop" && a.Kind != "season" && a.Kind != "still" && a.Kind != "logo" {
				continue
			}
			u := resolve(canonical(a, cfg.Quality), "")
			if u == "" {
				continue
			}
			item.Counts[a.Kind]++
			item.ArtworkCount++
			total++
			if total <= offset || total > offset+1000 {
				continue
			}
			item.Artwork = append(item.Artwork, setCatalogArtwork{ID: a.ID, Kind: a.Kind, URL: u, SetID: g.ID, Season: a.Season, Episode: a.Episode, Language: a.Language})
		}
		if item.ArtworkCount > 0 {
			out.Sets = append(out.Sets, item)
		}
		if len(out.Sets) >= 100 {
			out.HasMore = true
			break
		}
	}
	if total > offset+1000 {
		out.HasMore = true
		out.NextOffset = offset + 1000
	}
	return out, nil
}

func (s *server) handleSets(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	if r.GetHeaders()["X-Silo-User-Role"] != "admin" {
		return settingsError(403, "Administrator access required")
	}
	switch {
	case r.GetMethod() == http.MethodGet && r.GetPath() == "/sets":
		return s.handleArtworkManager(ctx, r)
	case r.GetMethod() == http.MethodGet && r.GetPath() == "/sets/catalog":
		f := r.GetQuery().GetFields()
		typ, id := f["type"].GetStringValue(), f["id"].GetStringValue()
		if (typ != "movie" && typ != "series") || !validSetID(id) {
			return settingsError(400, "Invalid set target")
		}
		offset := 0
		if raw := f["offset"].GetStringValue(); raw != "" {
			var err error
			offset, err = strconv.Atoi(raw)
			if err != nil || offset < 0 || offset > 100000 {
				return settingsError(400, "Invalid artwork offset")
			}
		}
		v, e := s.setCatalogPage(ctx, typ, id, offset)
		if e != nil {
			return settingsError(502, "Set catalog unavailable")
		}
		b, _ := json.Marshal(map[string]any{"type": typ, "id": id, "sets": v.Sets, "has_more": v.HasMore, "next_offset": v.NextOffset, "preferred_set_id": s.preferredSet(typ, id)})
		return settingsResponse(200, b, "application/json"), nil
	case r.GetMethod() == http.MethodGet && r.GetPath() == "/sets/preferences":
		s.settingsMu.Lock()
		m := map[string]string{}
		for k, v := range s.setBindings {
			m[k] = v
		}
		c, e := s.settingsCSRFLocked()
		s.settingsMu.Unlock()
		if e != nil {
			return settingsError(500, "Could not prepare set preferences")
		}
		b, _ := json.Marshal(map[string]any{"bindings": m, "csrf": c})
		return settingsResponse(200, b, "application/json"), nil
	case r.GetMethod() == http.MethodPut && r.GetPath() == "/sets/preferences":
		return s.saveSetPreference(ctx, r)
	case r.GetPath() == "/sets/preferences":
		return settingsError(405, "Method not allowed")
	}
	return settingsError(404, "Not found")
}

func (s *server) saveSetPreference(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	if len(r.GetBody()) > maxSettingsBody {
		return settingsError(413, "Set preference payload is too large")
	}
	d := json.NewDecoder(bytes.NewReader(r.GetBody()))
	d.DisallowUnknownFields()
	var in struct {
		CSRF  string `json:"csrf"`
		Type  string `json:"type"`
		ID    string `json:"id"`
		SetID string `json:"set_id"`
	}
	if d.Decode(&in) != nil {
		return settingsError(400, "Invalid set preference")
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return settingsError(400, "Invalid set preference")
	}
	if !validSetType(in.Type) || !validCollectionID(in.ID) || in.SetID != "" && !validSetID(in.SetID) || (in.Type != "collection" && !validSetID(in.ID)) {
		return settingsError(400, "Invalid set preference")
	}
	s.settingsMu.Lock()
	c, e := s.settingsCSRFLocked()
	s.settingsMu.Unlock()
	if e != nil || subtle.ConstantTimeCompare([]byte(in.CSRF), []byte(c)) != 1 {
		return settingsError(403, "Invalid CSRF token")
	}
	if in.SetID != "" {
		if in.Type == "collection" {
			if _, _, e := s.collectionSnapshot(ctx, in.ID); e != nil {
				return settingsError(400, "Collection target unavailable")
			}
			a, e := s.p.collectionSetCovers(ctx, in.SetID)
			if e != nil || len(a.Items) == 0 {
				return settingsError(502, "Set has no collection artwork")
			}
		} else {
			b, e := s.p.get(ctx, in.Type, in.ID)
			if e != nil {
				return settingsError(502, "Set catalog unavailable")
			}
			cfg := s.p.snapshot()
			priority, excluded := splitSet(cfg.Priority), splitSet(cfg.Excluded)
			found := false
			for _, group := range b.Sets {
				if group.ID != in.SetID {
					continue
				}
				_, bad := excluded[strings.ToLower(group.Creator)]
				_, ranked := priority[strings.ToLower(group.Creator)]
				if bad || cfg.OnlyPriority && !ranked {
					break
				}
				for _, a := range group.Art {
					if resolve(canonical(a, cfg.Quality), "") != "" {
						found = true
						break
					}
				}
				break
			}
			if !found {
				return settingsError(400, "Set is not available for this item")
			}
		}
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	next := map[string]string{}
	for k, v := range s.setBindings {
		next[k] = v
	}
	k := setBindingKey(in.Type, in.ID)
	if in.SetID == "" {
		delete(next, k)
	} else {
		next[k] = in.SetID
	}
	if len(next) > 500 {
		return settingsError(400, "At most 500 set preferences are allowed")
	}
	if s.saveSetPreferences == nil {
		return settingsError(500, "Set preference persistence unavailable")
	}
	if e := s.saveSetPreferences(ctx, next); e != nil {
		return settingsError(500, "Could not save set preference")
	}
	s.setBindings = next
	b, _ := json.Marshal(map[string]any{"bindings": next, "csrf": c})
	return settingsResponse(200, b, "application/json"), nil
}
