package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/manifest"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtime"
	"github.com/Silo-Server/silo-plugin-sdk/pkg/pluginsdk/runtimedefault"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

var version string

const graphQLEndpoint = "https://images.mediux.io/graphql"
const imageHost = "https://images.mediux.io/assets/"
const imageRequestPurposeHeader = "silo-image-request-purpose"

var assetID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type settings struct {
	Token, Priority, Excluded, Selection, Quality           string
	SeasonConsistency, TitlecardConsistency, MissingArtwork string
	OnlyPriority                                            bool
	CacheHours                                              int
}

func (s settings) ttl() time.Duration {
	if s.CacheHours <= 0 {
		return 0
	}
	return time.Duration(s.CacheHours) * time.Hour
}
func splitSet(s string) map[string]int {
	out := map[string]int{}
	for i, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(strings.ToLower(x)); x != "" {
			out[x] = i
		}
	}
	return out
}

type artwork struct {
	ID, Creator, Kind, SetID string
	Season, Episode          int
	Popularity               float64
	Modified                 time.Time
	Rank                     int
	Language                 string
}
type set struct {
	ID, Creator string
	Popularity  float64
	Art         []artwork
}
type bundle struct{ Sets []set }
type cacheEntry struct {
	b     bundle
	until time.Time
}
type provider struct {
	mu         sync.Mutex
	cfg        settings
	cache      map[string]cacheEntry
	flight     map[string]*call
	generation uint64
	endpoint   string
	client     *http.Client
	sem        chan struct{}
}
type call struct {
	done chan struct{}
	b    bundle
	err  error
}

func newProvider() *provider {
	return &provider{cfg: settings{Selection: "BestOnly", Quality: "optimized", CacheHours: 24, SeasonConsistency: "MostComplete", TitlecardConsistency: "MostComplete", MissingArtwork: "Fallback"}, cache: map[string]cacheEntry{}, flight: map[string]*call{}, sem: make(chan struct{}, 4), endpoint: graphQLEndpoint, client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (p *provider) configure(m map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.cfg
	if v, ok := m["api_token"].(string); ok {
		s.Token = v
	}
	if v, ok := m["priority_creators"].(string); ok {
		s.Priority = v
	}
	if v, ok := m["excluded_creators"].(string); ok {
		s.Excluded = v
	}
	if v, ok := m["only_priority_creators"].(bool); ok {
		s.OnlyPriority = v
	}
	if v, ok := m["image_selection"].(string); ok && v != "" {
		s.Selection = v
	}
	if v, ok := m["quality"].(string); ok && v != "" {
		s.Quality = v
	}
	if v, ok := m["season_consistency"].(string); ok && v != "" {
		s.SeasonConsistency = v
	}
	if v, ok := m["titlecard_consistency"].(string); ok && v != "" {
		s.TitlecardConsistency = v
	}
	if v, ok := m["missing_artwork"].(string); ok && v != "" {
		s.MissingArtwork = v
	}
	if v, ok := m["cache_hours"].(float64); ok {
		s.CacheHours = int(v)
	}
	if v, ok := m["cache_hours"].(int); ok {
		s.CacheHours = v
	}
	p.cfg = s
	p.cache = map[string]cacheEntry{}
	p.generation++
}
func (p *provider) snapshot() settings { p.mu.Lock(); defer p.mu.Unlock(); return p.cfg }
func (p *provider) get(ctx context.Context, typ, id string) (bundle, error) {
	p.mu.Lock()
	key := fmt.Sprintf("%d:%s:%s", p.generation, typ, id)
	if e, ok := p.cache[key]; ok && time.Now().Before(e.until) {
		p.mu.Unlock()
		return e.b, nil
	}
	if c := p.flight[key]; c != nil {
		p.mu.Unlock()
		select {
		case <-c.done:
			return c.b, c.err
		case <-ctx.Done():
			return bundle{}, ctx.Err()
		}
	}
	if len(p.flight) >= 4 {
		p.mu.Unlock()
		return bundle{}, errors.New("Mediux requests busy")
	}
	c := &call{done: make(chan struct{})}
	gen := p.generation
	p.flight[key] = c
	p.mu.Unlock()
	b, err := p.fetch(ctx, typ, id)
	p.mu.Lock()
	if err == nil && gen == p.generation && p.cfg.CacheHours != 0 {
		if len(p.cache) >= 128 {
			var oldest string
			var until time.Time
			for k, v := range p.cache {
				if oldest == "" || v.until.Before(until) {
					oldest = k
					until = v.until
				}
			}
			delete(p.cache, oldest)
		}
		ttl := p.cfg.ttl()
		if len(b.Sets) == 0 && ttl > 5*time.Minute {
			ttl = 5 * time.Minute
		}
		p.cache[key] = cacheEntry{b, time.Now().Add(ttl)}
	}
	delete(p.flight, key)
	c.b, c.err = b, err
	close(c.done)
	p.mu.Unlock()
	return b, err
}

func (p *provider) resolveTVDB(ctx context.Context, typ, tvdb string) (string, error) {
	if tvdb == "" {
		return "", nil
	}
	cfg := p.snapshot()
	if strings.TrimSpace(cfg.Token) == "" {
		return "", errors.New("Mediux API token is not configured")
	}
	entity, query := "shows", `query($tvdb_id:String!){shows(filter:{tvdb_id:{_eq:$tvdb_id}}){id}}`
	if typ == "movie" {
		entity = "movies"
		query = `query($tvdb_id:String!){movies(filter:{tvdb_id:{_eq:$tvdb_id}}){id}}`
	}
	body, _ := json.Marshal(map[string]any{"query": query, "variables": map[string]string{"tvdb_id": tvdb}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-p.sem }()
	res, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (2<<20)+1))
	if err != nil {
		return "", err
	}
	if len(raw) > 2<<20 {
		return "", errors.New("Mediux API response exceeds size limit")
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("Mediux API status %d", res.StatusCode)
	}
	var out struct {
		Data map[string][]struct {
			ID string `json:"id"`
		} `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Errors) > 0 {
		return "", graphqlError(out.Errors, "TVDB "+typ+" lookup", cfg.Token)
	}
	if len(out.Data[entity]) == 0 {
		return "", nil
	}
	return out.Data[entity][0].ID, nil
}

const showQuery = `query($tmdb_id: ID!){shows_by_id(id:$tmdb_id){id show_sets(limit:-1){ id popularity user_created{username} show_poster(limit:-1){id modified_on language{display_name}} show_backdrop(limit:-1){id modified_on language{display_name}} season_posters(limit:-1){ id modified_on language{display_name} season{season_number}} titlecards(limit:-1){id modified_on language{display_name} episode{episode_number season_id{season_number}}} files(limit:-1){ id file_type modified_on language{display_name} }}}}`
const movieQuery = `query($tmdb_id: ID!){movies_by_id(id:$tmdb_id){id movie_sets(limit:-1){id popularity user_created{username} movie_poster(limit:-1){id modified_on language{display_name}} movie_backdrop(limit:-1){id modified_on language{display_name}} files(limit:-1){id file_type modified_on language{display_name}}} posters(limit:-1,filter:{collection_set:{id:{_nnull:true}}}){id modified_on language{display_name} movie{id} collection_set{id popularity user_created{username}}} backdrops(limit:-1,filter:{collection_set:{id:{_nnull:true}}}){id modified_on language{display_name} movie{id} collection_set{id popularity user_created{username}}}}}`

func (p *provider) fetch(ctx context.Context, typ, id string) (bundle, error) {
	cfg := p.snapshot()
	if strings.TrimSpace(cfg.Token) == "" {
		return bundle{}, errors.New("Mediux API token is not configured")
	}
	q := movieQuery
	if typ == "series" {
		q = showQuery
	}
	body, _ := json.Marshal(map[string]any{"query": q, "variables": map[string]string{"tmdb_id": id}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return bundle{}, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return bundle{}, ctx.Err()
	}
	defer func() { <-p.sem }()
	res, err := p.client.Do(req)
	if err != nil {
		return bundle{}, err
	}
	defer res.Body.Close()
	const maxResponse = 2 << 20
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return bundle{}, err
	}
	if len(raw) > maxResponse {
		return bundle{}, errors.New("Mediux API response exceeds size limit")
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return bundle{}, fmt.Errorf("Mediux API status %d", res.StatusCode)
	}
	var g struct {
		Data   json.RawMessage   `json:"data"`
		Errors []json.RawMessage `json:"errors"`
	}
	if err = json.Unmarshal(raw, &g); err != nil {
		return bundle{}, err
	}
	if len(g.Errors) > 0 {
		return bundle{}, graphqlError(g.Errors, typ+" artwork", cfg.Token)
	}
	return parseBundle(g.Data, typ, id)
}
func parseBundle(raw json.RawMessage, typ, expectedID string) (bundle, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return bundle{}, err
	}
	key := "movies_by_id"
	setsKey := "movie_sets"
	if typ == "series" {
		key = "shows_by_id"
		setsKey = "show_sets"
	}
	if string(root[key]) == "null" {
		return bundle{}, nil
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(root[key], &item); err != nil {
		return bundle{}, err
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(root[key], &identity); err != nil {
		return bundle{}, err
	}
	if identity.ID != "" && identity.ID != expectedID {
		return bundle{}, errors.New("Mediux identity mismatch")
	}
	var rows []struct {
		ID         string  `json:"id"`
		Popularity float64 `json:"popularity"`
		User       struct {
			Username string `json:"username"`
		} `json:"user_created"`
		MoviePoster []struct {
			ID       string `json:"id"`
			Modified string `json:"modified_on"`
			Language struct {
				DisplayName string `json:"display_name"`
			} `json:"language"`
		} `json:"movie_poster"`
		MovieBackdrop []struct {
			ID       string `json:"id"`
			Modified string `json:"modified_on"`
			Language struct {
				DisplayName string `json:"display_name"`
			} `json:"language"`
		} `json:"movie_backdrop"`
		ShowPoster []struct {
			ID       string `json:"id"`
			Modified string `json:"modified_on"`
			Language struct {
				DisplayName string `json:"display_name"`
			} `json:"language"`
		} `json:"show_poster"`
		ShowBackdrop []struct {
			ID       string `json:"id"`
			Modified string `json:"modified_on"`
			Language struct {
				DisplayName string `json:"display_name"`
			} `json:"language"`
		} `json:"show_backdrop"`
		SeasonPosters []struct {
			ID       string `json:"id"`
			Modified string `json:"modified_on"`
			Language struct {
				DisplayName string `json:"display_name"`
			} `json:"language"`
			Season *struct {
				Number int `json:"season_number"`
			} `json:"season"`
		} `json:"season_posters"`
		Titlecards []struct {
			ID       string `json:"id"`
			Modified string `json:"modified_on"`
			Language struct {
				DisplayName string `json:"display_name"`
			} `json:"language"`
			Episode struct {
				Number int `json:"episode_number"`
				Season *struct {
					Number int `json:"season_number"`
				} `json:"season_id"`
			} `json:"episode"`
		} `json:"titlecards"`
		Files []struct {
			ID       string `json:"id"`
			Type     string `json:"file_type"`
			Modified string `json:"modified_on"`
			Language struct {
				DisplayName string `json:"display_name"`
			} `json:"language"`
		} `json:"files"`
	}
	if err := json.Unmarshal(item[setsKey], &rows); err != nil {
		return bundle{}, err
	}
	b := bundle{}
	for _, r := range rows {
		s := set{r.ID, r.User.Username, r.Popularity, nil}
		add := func(id, modified, language, k string, season, episode int) {
			if id != "" {
				at, _ := time.Parse(time.RFC3339Nano, modified)
				s.Art = append(s.Art, artwork{ID: id, Creator: r.User.Username, Kind: k, SetID: r.ID, Season: season, Episode: episode, Popularity: r.Popularity, Modified: at, Language: normalizeLanguage(language)})
			}
		}
		for _, a := range r.MoviePoster {
			add(a.ID, a.Modified, a.Language.DisplayName, "poster", 0, 0)
		}
		for _, a := range r.MovieBackdrop {
			add(a.ID, a.Modified, a.Language.DisplayName, "backdrop", 0, 0)
		}
		for _, a := range r.ShowPoster {
			add(a.ID, a.Modified, a.Language.DisplayName, "poster", 0, 0)
		}
		for _, a := range r.ShowBackdrop {
			add(a.ID, a.Modified, a.Language.DisplayName, "backdrop", 0, 0)
		}
		for _, a := range r.SeasonPosters {
			if a.Season != nil {
				add(a.ID, a.Modified, a.Language.DisplayName, "season", a.Season.Number, 0)
			}
		}
		for _, a := range r.Titlecards {
			if a.Episode.Season != nil {
				add(a.ID, a.Modified, a.Language.DisplayName, "still", a.Episode.Season.Number, a.Episode.Number)
			}
		}
		for _, a := range r.Files {
			if a.Type == "logo" {
				add(a.ID, a.Modified, a.Language.DisplayName, "logo", 0, 0)
			}
		}
		b.Sets = append(b.Sets, s)
	}
	if typ == "movie" {
		collections, err := movieCollectionSets(item, expectedID)
		if err != nil {
			return bundle{}, err
		}
		b.Sets = append(b.Sets, collections...)
	}
	return b, nil
}
func (p *provider) selectArt(b bundle, cfg settings, kind string, season, episode int, all bool) []artwork {
	priority, excluded := splitSet(cfg.Priority), splitSet(cfg.Excluded)
	var out []artwork
	for _, s := range b.Sets {
		rank, ok := priority[strings.ToLower(s.Creator)]
		if _, bad := excluded[strings.ToLower(s.Creator)]; bad || cfg.OnlyPriority && !ok {
			continue
		}
		for _, a := range s.Art {
			if a.Kind != kind || a.Season != season || a.Episode != episode {
				continue
			}
			a.Popularity = s.Popularity
			a.Rank = 99999999
			if ok {
				a.Rank = rank
			}
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		if out[i].Popularity != out[j].Popularity {
			return out[i].Popularity > out[j].Popularity
		}
		return out[i].ID < out[j].ID
	})
	seen := map[string]bool{}
	dedup := out[:0]
	for _, a := range out {
		if !seen[a.ID] {
			seen[a.ID] = true
			dedup = append(dedup, a)
		}
	}
	out = dedup
	if !all && len(out) > 1 {
		return out[:1]
	}
	return out
}

// bestForLanguage keeps provider ranking inside each Silo-facing language tier.
func bestForLanguage(candidates []artwork, kind, requested string) []artwork {
	if len(candidates) == 0 {
		return nil
	}
	// Titlecards keep creator priority before language, including gap fallback.
	// selectArt supplies candidates ordered by creator rank.
	if kind == "still" {
		end := 1
		for end < len(candidates) && candidates[end].Rank == candidates[0].Rank {
			end++
		}
		candidates = candidates[:end]
	}
	requested = normalizeLanguage(requested)
	tiers := []func(string) bool{}
	switch kind {
	case "poster", "season":
		if requested != "" {
			tiers = append(tiers, func(l string) bool { return l == requested })
		}
		if requested != "en" {
			tiers = append(tiers, func(l string) bool { return l == "en" })
		}
		tiers = append(tiers, func(l string) bool { return l != "" }, func(l string) bool { return l == "" })
	case "logo", "still":
		if requested != "" {
			tiers = append(tiers, func(l string) bool { return l == requested })
		}
		if requested != "en" {
			tiers = append(tiers, func(l string) bool { return l == "en" })
		}
		tiers = append(tiers, func(l string) bool { return l == "" }, func(l string) bool { return l != "" })
	default: // backdrops favor neutral art, then any tagged art.
		tiers = append(tiers, func(l string) bool { return l == "" }, func(l string) bool { return l != "" })
	}
	for _, match := range tiers {
		for _, a := range candidates {
			if match(a.Language) {
				return []artwork{a}
			}
		}
	}
	return nil
}

// seriesSet returns the set selected for the series poster under the current
// language and creator rules. Silo has no persistent set binding, so this is
// intentionally deterministic per metadata request.
func (p *provider) seriesSet(b bundle, cfg settings, language string) string {
	posters := p.selectArt(b, cfg, "poster", 0, 0, true)
	chosen := bestForLanguage(posters, "poster", language)
	if len(chosen) == 0 {
		return ""
	}
	return chosen[0].SetID
}

// automaticArt applies separate season and titlecard set preferences.
// Galleries deliberately use selectArt directly.
func (p *provider) automaticArt(b bundle, cfg settings, kind string, season, episode int, language string) []artwork {
	return p.automaticArtWithSet(b, cfg, kind, season, episode, language, p.artworkSet(b, cfg, kind, language))
}

type setSelection struct {
	ID     string
	Strict bool
}

// artworkSet is computed once for a season/episode response, rather than once
// per image. This also keeps the whole response on the same selected set.
func (p *provider) artworkSet(b bundle, cfg settings, kind, language string) setSelection {
	mode := "SeriesPoster"
	if kind == "season" && cfg.SeasonConsistency != "" {
		mode = cfg.SeasonConsistency
	} else if kind == "still" && cfg.TitlecardConsistency != "" {
		mode = cfg.TitlecardConsistency
	}
	selection := setSelection{Strict: (kind == "season" || kind == "still") && mode != "Independent" && cfg.MissingArtwork == "KeepExisting"}
	switch mode {
	case "MostComplete", "CreatorPriority":
		selection.ID = p.consistentSet(b, cfg, kind, language)
	case "SeriesPoster":
		selection.ID = p.seriesSet(b, cfg, language)
	}
	return selection
}

func (p *provider) automaticArtWithSet(b bundle, cfg settings, kind string, season, episode int, language string, selection setSelection) []artwork {
	global := p.selectArt(b, cfg, kind, season, episode, true)
	if len(global) == 0 {
		return nil
	}
	if selection.ID != "" {
		local := make([]artwork, 0, len(global))
		for _, a := range global {
			if a.SetID == selection.ID {
				local = append(local, a)
			}
		}
		if len(local) > 0 {
			if chosen := bestForLanguage(local, kind, language); len(chosen) > 0 {
				return chosen
			}
		}
	}
	if selection.Strict {
		return nil
	}
	return bestForLanguage(global, kind, language)
}
func canonical(a artwork, quality string) string {
	v := a.Modified.UTC().Format("20060102150405")
	if v == "00010101000000" {
		v = "0"
	}
	q := "jpg"
	if quality == "original" || a.Kind == "logo" {
		q = "original"
	}
	return "mediux://" + a.ID + "?v=" + v + "&quality=" + q
}
func normalizeLanguage(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if len(n) == 2 {
		return n
	}
	for _, pair := range []struct{ name, code string }{{"english", "en"}, {"spanish", "es"}, {"french", "fr"}, {"german", "de"}, {"italian", "it"}, {"portuguese", "pt"}, {"brazilian portuguese", "pt-br"}, {"japanese", "ja"}, {"korean", "ko"}, {"chinese", "zh"}, {"simplified chinese", "zh-cn"}, {"traditional chinese", "zh-tw"}, {"russian", "ru"}, {"arabic", "ar"}, {"hindi", "hi"}, {"dutch", "nl"}, {"polish", "pl"}, {"turkish", "tr"}, {"swedish", "sv"}, {"norwegian", "no"}, {"danish", "da"}, {"finnish", "fi"}} {
		if n == pair.name || n == pair.code {
			return pair.code
		}
	}
	return ""
}
func resolve(path, variant string) string {
	u, err := url.Parse(path)
	if err != nil || (u.Scheme != "" && u.Scheme != "mediux") {
		return ""
	}
	id := u.Host
	if id == "" {
		id = strings.TrimPrefix(u.Path, "/")
	}
	if !assetID.MatchString(id) {
		return ""
	}
	q := u.Query().Get("quality")
	if q != "original" {
		q = "jpg"
	}
	_ = variant // quality binds canonical source; unknown variants use same safe URL.
	values := url.Values{}
	if q != "original" {
		values.Set("key", q)
	}
	if v := u.Query().Get("v"); v != "" && regexp.MustCompile(`^[0-9]{1,14}$`).MatchString(v) {
		values.Set("v", v)
	}
	return imageHost + id + "?" + values.Encode()
}

//go:embed manifest.json
var manifestJSON []byte

type server struct {
	runtimedefault.Server
	pluginv1.UnimplementedMetadataProviderServer
	pluginv1.UnimplementedImageResolverServer
	pluginv1.UnimplementedHttpRoutesServer
	m               *pluginv1.PluginManifest
	p               *provider
	settingsMu      sync.Mutex
	settingsCSRF    string
	savePreferences func(context.Context, map[string]any) error
}

func (s *server) GetManifest(context.Context, *pluginv1.GetManifestRequest) (*pluginv1.GetManifestResponse, error) {
	return &pluginv1.GetManifestResponse{Manifest: s.m}, nil
}
func (s *server) Configure(_ context.Context, r *pluginv1.ConfigureRequest) (*pluginv1.ConfigureResponse, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	values := map[string]any{}
	preferences := map[string]any{}
	for _, entry := range r.GetConfig() {
		if entry.GetKey() == "settings" && entry.GetValue() != nil {
			for k, v := range entry.GetValue().AsMap() {
				values[k] = v
			}
		}
		if entry.GetKey() == "artwork_preferences" && entry.GetValue() != nil {
			preferences = entry.GetValue().AsMap()
		}
	}
	for _, key := range []string{"priority_creators", "excluded_creators", "only_priority_creators", "season_consistency", "titlecard_consistency", "missing_artwork", "image_selection", "quality", "cache_hours"} {
		if v, exists := preferences[key]; exists {
			values[key] = v
		}
	}
	if token, ok := values["api_token"].(string); !ok || strings.TrimSpace(token) == "" {
		return nil, errors.New("api_token is required")
	}
	if v, ok := values["image_selection"].(string); ok && v != "BestOnly" && v != "All" {
		return nil, errors.New("image_selection must be BestOnly or All")
	}
	if v, ok := values["quality"].(string); ok && v != "optimized" && v != "original" {
		return nil, errors.New("quality must be optimized or original")
	}
	for _, key := range []string{"season_consistency", "titlecard_consistency"} {
		if v, exists := values[key]; exists {
			if mode, ok := v.(string); !ok || !validChoice(mode, "MostComplete", "CreatorPriority", "SeriesPoster", "Independent") {
				return nil, fmt.Errorf("%s must be MostComplete, CreatorPriority, SeriesPoster, or Independent", key)
			}
		}
	}
	if v, exists := values["missing_artwork"]; exists {
		if mode, ok := v.(string); !ok || (mode != "Fallback" && mode != "KeepExisting") {
			return nil, errors.New("missing_artwork must be Fallback or KeepExisting")
		}
	}
	if v, ok := values["cache_hours"].(float64); ok && (v < 0 || v > 168 || v != float64(int(v))) {
		return nil, errors.New("cache_hours must be an integer from 0 through 168")
	}
	s.p.configure(values)
	return &pluginv1.ConfigureResponse{}, nil
}
func (s *server) Search(context.Context, *pluginv1.SearchMetadataRequest) (*pluginv1.SearchMetadataResponse, error) {
	return &pluginv1.SearchMetadataResponse{}, nil
}
func ids(x *structpb.Struct) map[string]string {
	out := map[string]string{}
	if x != nil {
		for k, v := range x.AsMap() {
			if z, ok := v.(string); ok {
				out[k] = z
			}
		}
	}
	return out
}
func structFromMap(values map[string]any) *structpb.Struct {
	value, err := structpb.NewStruct(values)
	if err != nil {
		panic(err)
	}
	return value
}
func mediaID(t string, x *structpb.Struct, fallback string) (string, string) {
	m := ids(x)
	if z := m["tmdb"]; z != "" {
		return t, z
	}
	return t, ""
}
func (s *server) identity(ctx context.Context, typ string, x *structpb.Struct, fallback string) (string, string, error) {
	if typ != "series" && typ != "movie" {
		return typ, "", nil
	}
	m := ids(x)
	if own := m["mediux"]; own != "" {
		parts := strings.SplitN(own, ":", 2)
		if len(parts) == 2 && parts[0] == typ && numericID(parts[1]) {
			return typ, parts[1], nil
		}
		return typ, "", nil
	}
	if strings.HasPrefix(fallback, typ+":") {
		id := strings.TrimPrefix(fallback, typ+":")
		if numericID(id) {
			return typ, id, nil
		}
	}
	if id := m["tmdb"]; id != "" {
		if numericID(id) {
			return typ, id, nil
		}
		return typ, "", nil
	}
	if tvdb := m["tvdb"]; tvdb != "" {
		id, e := s.p.resolveTVDB(ctx, typ, tvdb)
		return typ, id, e
	}
	return typ, "", nil
}
func numericID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
func (s *server) GetMetadata(ctx context.Context, r *pluginv1.GetMetadataRequest) (*pluginv1.GetMetadataResponse, error) {
	typ, id, err := s.identity(ctx, r.GetItemType(), r.GetProviderIds(), r.GetProviderId())
	if err != nil {
		return nil, err
	}
	if id == "" {
		return &pluginv1.GetMetadataResponse{}, nil
	}
	st, _ := structpb.NewStruct(map[string]any{"tmdb": id, "mediux": typ + ":" + id})
	return &pluginv1.GetMetadataResponse{Item: &pluginv1.MetadataItem{ProviderId: typ + ":" + id, ItemType: r.GetItemType(), ProviderIds: st}}, nil
}
func (s *server) GetImages(ctx context.Context, r *pluginv1.GetImagesRequest) (*pluginv1.GetImagesResponse, error) {
	typ, id, err := s.identity(ctx, r.GetItemType(), r.GetProviderIds(), r.GetProviderId())
	if err != nil {
		return nil, err
	}
	if id == "" {
		return &pluginv1.GetImagesResponse{}, nil
	}
	b, e := s.p.get(ctx, typ, id)
	if e != nil {
		return nil, e
	}
	season, episode := 0, 0
	if r.SeasonNumber != nil {
		season = int(r.GetSeasonNumber())
	}
	kind := "poster"
	if season != 0 || r.SeasonNumber != nil {
		kind = "season"
	}
	cfg := s.p.snapshot()
	all := cfg.Selection == "All" || isGalleryImageRequest(ctx)
	as := s.p.selectArt(b, cfg, kind, season, episode, true)
	if kind == "poster" {
		backdrops := s.p.selectArt(b, cfg, "backdrop", 0, 0, true)
		logos := s.p.selectArt(b, cfg, "logo", 0, 0, true)
		if !all {
			as = s.p.automaticArt(b, cfg, "poster", 0, 0, r.GetLanguage())
			backdrops = s.p.automaticArt(b, cfg, "backdrop", 0, 0, r.GetLanguage())
			logos = s.p.automaticArt(b, cfg, "logo", 0, 0, r.GetLanguage())
		}
		as = append(as, backdrops...)
		as = append(as, logos...)
	} else if !all {
		as = s.p.automaticArt(b, cfg, "season", season, episode, r.GetLanguage())
	}
	o := &pluginv1.GetImagesResponse{}
	for _, a := range as {
		k := a.Kind
		if k == "season" {
			k = "poster"
		}
		record := &pluginv1.ImageRecord{Kind: k, Url: canonical(a, cfg.Quality), Language: a.Language}
		if a.Language != "" {
			record.Metadata = structFromMap(map[string]any{"includes_text": true})
		}
		if kind == "season" {
			n := int32(season)
			record.SeasonNumber = &n
		}
		o.Images = append(o.Images, record)
	}
	return o, nil
}

// isGalleryImageRequest recognizes the host's advisory picker-purpose header.
// Missing, malformed, or repeated values retain automatic selection behavior.
func isGalleryImageRequest(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return false
	}
	values := md.Get(imageRequestPurposeHeader)
	return len(values) == 1 && values[0] == "gallery"
}
func (s *server) GetSeasons(ctx context.Context, r *pluginv1.GetSeasonsRequest) (*pluginv1.GetSeasonsResponse, error) {
	_, id, err := s.identity(ctx, "series", r.GetProviderIds(), r.GetSeriesProviderId())
	if err != nil {
		return nil, err
	}
	if id == "" {
		return &pluginv1.GetSeasonsResponse{}, nil
	}
	b, e := s.p.get(ctx, "series", id)
	if e != nil {
		return nil, e
	}
	cfg := s.p.snapshot()
	selection := s.p.artworkSet(b, cfg, "season", r.GetLanguage())
	seen := map[int]bool{}
	o := &pluginv1.GetSeasonsResponse{}
	for _, set := range b.Sets {
		for _, a := range set.Art {
			if a.Kind == "season" && !seen[a.Season] {
				// A SeasonRecord is singular even when the image gallery is All.
				// Apply the configured season-set consistency policy.
				seen[a.Season] = true
				choices := s.p.automaticArtWithSet(b, cfg, "season", a.Season, 0, r.GetLanguage(), selection)
				if len(choices) > 0 {
					seen[a.Season] = true
					o.Seasons = append(o.Seasons, &pluginv1.SeasonRecord{SeasonNumber: int32(a.Season), PosterPath: canonical(choices[0], cfg.Quality)})
				}
			}
		}
	}
	sort.Slice(o.Seasons, func(i, j int) bool { return o.Seasons[i].SeasonNumber < o.Seasons[j].SeasonNumber })
	return o, nil
}
func (s *server) GetEpisodes(ctx context.Context, r *pluginv1.GetEpisodesRequest) (*pluginv1.GetEpisodesResponse, error) {
	_, id, err := s.identity(ctx, "series", r.GetProviderIds(), r.GetSeriesProviderId())
	if err != nil {
		return nil, err
	}
	if id == "" {
		return &pluginv1.GetEpisodesResponse{}, nil
	}
	b, e := s.p.get(ctx, "series", id)
	if e != nil {
		return nil, e
	}
	sn := int(r.GetSeasonNumber())
	o := &pluginv1.GetEpisodesResponse{}
	cfg := s.p.snapshot()
	selection := s.p.artworkSet(b, cfg, "still", r.GetLanguage())
	seen := map[int]bool{}
	for _, set := range b.Sets {
		for _, a := range set.Art {
			if a.Kind == "still" && a.Season == sn && !seen[a.Episode] {
				seen[a.Episode] = true
				choices := s.p.automaticArtWithSet(b, cfg, "still", sn, a.Episode, r.GetLanguage(), selection)
				if len(choices) == 0 {
					continue
				}
				seen[a.Episode] = true
				o.Episodes = append(o.Episodes, &pluginv1.EpisodeRecord{SeasonNumber: int32(sn), EpisodeNumber: int32(a.Episode), StillPath: canonical(choices[0], cfg.Quality)})
			}
		}
	}
	sort.Slice(o.Episodes, func(i, j int) bool { return o.Episodes[i].EpisodeNumber < o.Episodes[j].EpisodeNumber })
	return o, nil
}
func (s *server) ResolveImageURL(_ context.Context, r *pluginv1.ResolveImageURLRequest) (*pluginv1.ResolveImageURLResponse, error) {
	return &pluginv1.ResolveImageURLResponse{Url: resolve(r.GetPath(), r.GetVariant())}, nil
}
func (s *server) ResolveImageURLs(_ context.Context, r *pluginv1.ResolveImageURLsRequest) (*pluginv1.ResolveImageURLsResponse, error) {
	o := map[string]string{}
	for _, p := range r.GetPaths() {
		o[p] = resolve(p, r.GetVariant())
	}
	return &pluginv1.ResolveImageURLsResponse{Urls: o}, nil
}
func main() {
	m, e := manifest.Load(manifestJSON)
	if e != nil {
		panic(e)
	}
	if version != "" {
		m.Version = version
	}
	if path, e := os.Executable(); e == nil {
		if b, e := os.ReadFile(path); e == nil {
			h := sha256.Sum256(b)
			m.Checksum = hex.EncodeToString(h[:])
		}
	}
	s := &server{m: m, p: newProvider(), savePreferences: func(ctx context.Context, values map[string]any) error {
		host := runtime.Host()
		if host == nil {
			return errors.New("Silo settings service unavailable")
		}
		return host.SetGlobalConfigEntry(ctx, "artwork_preferences", values)
	}}
	runtime.Serve(runtime.ServeConfig{Servers: runtime.CapabilityServers{Runtime: s, MetadataProvider: s, ImageResolver: s, HttpRoutes: s}})
}
