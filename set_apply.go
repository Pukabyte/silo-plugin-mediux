package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

const setApplyBodyLimit = 16 << 10
const setApplyTokenLifetime = 15 * time.Minute
const setApplyOperationLimit = 1000

type setApplyPlanInput struct {
	CSRF      string `json:"csrf"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	ContentID string `json:"content_id,omitempty"`
	SetID     string `json:"set_id"`
	AssetID   string `json:"asset_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Season    *int   `json:"season,omitempty"`
	Episode   *int   `json:"episode,omitempty"`
}

var artworkTargetID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,511}$`)

type setApplyInput struct {
	CSRF  string `json:"csrf"`
	Token string `json:"token"`
}

type setApplyToken struct {
	TargetID  string `json:"target_id"`
	SourceURL string `json:"source_url"`
	ImageType string `json:"image_type"`
	Kind      string `json:"kind"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	SetID     string `json:"set_id"`
	Role      string `json:"role"`
	AssetID   string `json:"asset_id"`
	Nonce     string `json:"nonce"`
	ExpiresAt int64  `json:"expires_at"`
}

type setApplyOperation struct {
	Token    string `json:"token"`
	TargetID string `json:"target_id"`
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Label    string `json:"label"`
}

type setApplyCatalogItem struct {
	ContentID string `json:"content_id"`
	Type      string `json:"type"`
	TMDB      string `json:"tmdb_id"`
	TVDB      string `json:"tvdb_id"`
}

type setApplyCatalogChild struct {
	ContentID     string `json:"content_id"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
}

type setApplyCandidate struct {
	artwork artwork
	target  string
	label   string
}

func (s *server) handleSetApply(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	if r.GetHeaders()["X-Silo-User-Role"] != "admin" {
		return settingsError(http.StatusForbidden, "Administrator access required")
	}
	switch r.GetPath() {
	case "/sets/plan":
		if r.GetMethod() != http.MethodPost {
			return settingsError(http.StatusMethodNotAllowed, "Method not allowed")
		}
		return s.planSetApply(ctx, r.GetBody())
	case "/sets/apply":
		if r.GetMethod() != http.MethodPost {
			return settingsError(http.StatusMethodNotAllowed, "Method not allowed")
		}
		return s.applySetArtwork(ctx, r.GetBody())
	default:
		return settingsError(http.StatusNotFound, "Not found")
	}
}

func strictSetApplyJSON(raw []byte, out any) error {
	if len(raw) > setApplyBodyLimit {
		return errors.New("payload too large")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	return requireEOF(d)
}

func requireEOF(d *json.Decoder) error {
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (s *server) validSetApplyCSRF(csrf string) bool {
	s.settingsMu.Lock()
	expected, err := s.settingsCSRFLocked()
	s.settingsMu.Unlock()
	return err == nil && subtle.ConstantTimeCompare([]byte(csrf), []byte(expected)) == 1
}

func (s *server) planSetApply(ctx context.Context, raw []byte) (*pluginv1.HandleHTTPResponse, error) {
	var in setApplyPlanInput
	if err := strictSetApplyJSON(raw, &in); err != nil {
		return settingsError(http.StatusBadRequest, "Invalid artwork plan")
	}
	if !s.validSetApplyCSRF(in.CSRF) {
		return settingsError(http.StatusForbidden, "Invalid CSRF token")
	}
	if !validSetApplyInput(in) {
		return settingsError(http.StatusBadRequest, "Invalid artwork plan")
	}

	b, err := s.p.get(ctx, in.Type, in.ID)
	if err != nil {
		return settingsError(http.StatusBadGateway, "Set catalog unavailable")
	}
	group, ok := eligibleSet(b, s.p.snapshot(), in.SetID)
	if !ok {
		return settingsError(http.StatusBadRequest, "Set is not available for this item")
	}
	parent, err := s.resolveSetApplyParent(ctx, in)
	if err != nil {
		return setApplyPlanError(err)
	}
	candidates, skipped, err := s.setApplyCandidates(ctx, in, group, parent)
	if err != nil {
		return setApplyPlanError(err)
	}
	if len(candidates) > setApplyOperationLimit {
		return settingsError(http.StatusBadRequest, "Artwork plan exceeds the 1000 item limit")
	}
	ops := make([]setApplyOperation, 0, len(candidates))
	for _, candidate := range candidates {
		source := resolve(canonical(candidate.artwork, s.p.snapshot().Quality), "")
		imageType := nativeImageType(candidate.artwork.Kind)
		if !fixedMediuxHTTPS(source, candidate.artwork.ID) || imageType == "" {
			skipped++
			continue
		}
		claim := setApplyToken{TargetID: candidate.target, SourceURL: source, ImageType: imageType, Kind: candidate.artwork.Kind, Type: in.Type, ID: in.ID, SetID: in.SetID, Role: "admin", AssetID: candidate.artwork.ID, ExpiresAt: time.Now().Add(setApplyTokenLifetime).Unix()}
		token, err := s.signSetApplyToken(claim)
		if err != nil {
			return settingsError(http.StatusInternalServerError, "Could not prepare artwork plan")
		}
		ops = append(ops, setApplyOperation{Token: token, TargetID: candidate.target, Kind: candidate.artwork.Kind, URL: source, Label: candidate.label})
	}
	if len(ops) > setApplyOperationLimit {
		return settingsError(http.StatusBadRequest, "Artwork plan exceeds the 1000 item limit")
	}
	body, _ := json.Marshal(map[string]any{"type": in.Type, "id": in.ID, "content_id": parent, "set_id": in.SetID, "operations": ops, "skipped": skipped})
	return settingsResponse(http.StatusOK, body, "application/json"), nil
}

func validSetApplyInput(in setApplyPlanInput) bool {
	if (in.Type != "movie" && in.Type != "series") || !numericID(in.ID) || len(in.ID) > 20 || !validSetID(in.SetID) || (in.ContentID != "" && !validArtworkTargetID(in.ContentID)) {
		return false
	}
	if in.AssetID != "" && !assetID.MatchString(in.AssetID) {
		return false
	}
	if in.Kind != "" && in.Kind != "poster" && in.Kind != "backdrop" && in.Kind != "logo" && in.Kind != "season" && in.Kind != "still" {
		return false
	}
	if in.Season != nil && (*in.Season < 0 || *in.Season > 10000) || in.Episode != nil && (*in.Episode < 0 || *in.Episode > 100000) {
		return false
	}
	return true
}

func validArtworkTargetID(id string) bool { return artworkTargetID.MatchString(id) }

func eligibleSet(b bundle, cfg settings, id string) (set, bool) {
	priority, excluded := splitSet(cfg.Priority), splitSet(cfg.Excluded)
	for _, group := range b.Sets {
		if group.ID != id {
			continue
		}
		_, excluded := excluded[strings.ToLower(group.Creator)]
		_, preferred := priority[strings.ToLower(group.Creator)]
		if excluded || cfg.OnlyPriority && !preferred {
			return set{}, false
		}
		return group, true
	}
	return set{}, false
}

func (s *server) resolveSetApplyParent(ctx context.Context, in setApplyPlanInput) (string, error) {
	parent := in.ContentID
	if parent == "" {
		parent = in.Type + "-tmdb-" + in.ID
	}
	raw, _, _, err := s.catalogRead(ctx, "/api/v2/catalog/items/"+url.PathEscape(parent))
	if err != nil {
		return "", err
	}
	var item setApplyCatalogItem
	if json.Unmarshal(raw, &item) != nil || item.Type != in.Type || !validArtworkTargetID(item.ContentID) {
		return "", collectionError(http.StatusBadRequest, "The selected library item is unavailable")
	}
	tmdb := item.TMDB
	if !numericID(tmdb) && numericID(item.TVDB) {
		mapped, err := s.p.resolveTVDB(ctx, in.Type, item.TVDB)
		if err != nil {
			return "", collectionError(http.StatusBadGateway, "Could not identify this title in Mediux")
		}
		tmdb = mapped
	}
	if tmdb != in.ID {
		return "", collectionError(http.StatusBadRequest, "The selected library item does not match this Mediux title")
	}
	return item.ContentID, nil
}

func setApplyPlanError(err error) (*pluginv1.HandleHTTPResponse, error) {
	var native *collectionFailure
	if errors.As(err, &native) {
		return settingsError(native.status, native.message)
	}
	return settingsError(http.StatusBadGateway, "Could not prepare artwork plan")
}

func (s *server) setApplyCandidates(ctx context.Context, in setApplyPlanInput, group set, parent string) ([]setApplyCandidate, int, error) {
	filtered := make([]artwork, 0, len(group.Art))
	for _, art := range group.Art {
		if art.ID == "" || !assetID.MatchString(art.ID) || (in.AssetID != "" && art.ID != in.AssetID) || (in.Kind != "" && art.Kind != in.Kind) || (in.Season != nil && art.Season != *in.Season) || (in.Episode != nil && art.Episode != *in.Episode) {
			continue
		}
		if art.Kind != "poster" && art.Kind != "backdrop" && art.Kind != "logo" && art.Kind != "season" && art.Kind != "still" {
			continue
		}
		if in.Type == "movie" && (art.Kind == "season" || art.Kind == "still") {
			continue
		}
		filtered = append(filtered, art)
	}
	if in.AssetID != "" && len(filtered) == 0 {
		return nil, 0, collectionError(http.StatusBadRequest, "Selected artwork is not available in this set")
	}
	chosen := chooseSetApplyVariants(filtered)
	needsSeasons := map[int]bool{}
	needsEpisodes := map[int]bool{}
	for _, art := range chosen {
		if art.Kind == "season" {
			needsSeasons[art.Season] = true
		}
		if art.Kind == "still" {
			needsSeasons[art.Season] = true
			needsEpisodes[art.Season] = true
		}
	}
	seasonTargets, episodeTargets, err := s.resolveSetApplyChildren(ctx, parent, needsSeasons, needsEpisodes)
	if err != nil {
		return nil, 0, err
	}
	result := make([]setApplyCandidate, 0, len(chosen))
	skipped := 0
	for _, art := range chosen {
		target, label := parent, in.Type+" "+art.Kind
		switch art.Kind {
		case "season":
			target = seasonTargets[art.Season]
			label = "Season " + jsonNumber(art.Season) + " poster"
		case "still":
			target = episodeTargets[childSlot(art.Season, art.Episode)]
			label = "Season " + jsonNumber(art.Season) + " episode " + jsonNumber(art.Episode) + " still"
		}
		if !validArtworkTargetID(target) {
			skipped++
			continue
		}
		result = append(result, setApplyCandidate{artwork: art, target: target, label: label})
	}
	return result, skipped, nil
}

func chooseSetApplyVariants(art []artwork) []artwork {
	groups := map[string][]artwork{}
	keys := make([]string, 0)
	for _, a := range art {
		key := a.Kind
		if a.Kind == "season" {
			key += ":" + jsonNumber(a.Season)
		} else if a.Kind == "still" {
			key += ":" + jsonNumber(a.Season) + ":" + jsonNumber(a.Episode)
		}
		if _, ok := groups[key]; !ok {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], a)
	}
	sort.Strings(keys)
	out := make([]artwork, 0, len(keys))
	for _, key := range keys {
		if selected := bestForLanguage(groups[key], groups[key][0].Kind, "en"); len(selected) == 1 {
			out = append(out, selected[0])
		}
	}
	return out
}

func jsonNumber(n int) string {
	return strconv.Itoa(n)
}

func childSlot(season, episode int) string { return strconv.Itoa(season) + ":" + strconv.Itoa(episode) }

func (s *server) resolveSetApplyChildren(ctx context.Context, parent string, neededSeasons, neededEpisodes map[int]bool) (map[int]string, map[string]string, error) {
	seasons := map[int]string{}
	episodes := map[string]string{}
	if len(neededSeasons) == 0 {
		return seasons, episodes, nil
	}
	raw, _, _, err := s.catalogRead(ctx, "/api/v2/catalog/series/"+url.PathEscape(parent)+"/seasons")
	if err != nil {
		return nil, nil, err
	}
	var seasonData struct {
		Items []setApplyCatalogChild `json:"items"`
	}
	if json.Unmarshal(raw, &seasonData) != nil {
		return nil, nil, collectionError(http.StatusBadGateway, "Invalid season catalog response")
	}
	for _, child := range seasonData.Items {
		if neededSeasons[child.SeasonNumber] && validArtworkTargetID(child.ContentID) {
			seasons[child.SeasonNumber] = child.ContentID
		}
	}
	for season := range neededEpisodes {
		if !validArtworkTargetID(seasons[season]) {
			continue
		}
		raw, _, _, err := s.catalogRead(ctx, "/api/v2/catalog/series/"+url.PathEscape(parent)+"/seasons/"+jsonNumber(season)+"/episodes")
		if err != nil {
			return nil, nil, err
		}
		var episodeData struct {
			Items []setApplyCatalogChild `json:"items"`
		}
		if json.Unmarshal(raw, &episodeData) != nil {
			return nil, nil, collectionError(http.StatusBadGateway, "Invalid episode catalog response")
		}
		for _, child := range episodeData.Items {
			if child.SeasonNumber == season && validArtworkTargetID(child.ContentID) {
				episodes[childSlot(season, child.EpisodeNumber)] = child.ContentID
			}
		}
	}
	return seasons, episodes, nil
}

func nativeImageType(kind string) string {
	switch kind {
	case "poster", "season":
		return "poster"
	case "backdrop", "logo", "still":
		return kind
	default:
		return ""
	}
}

func fixedMediuxHTTPS(source, asset string) bool {
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Host != "images.mediux.io" || u.User != nil || u.Fragment != "" || !assetID.MatchString(asset) || u.EscapedPath() != "/assets/"+asset {
		return false
	}
	query := u.Query()
	for key, values := range query {
		if (key != "key" && key != "v") || len(values) != 1 {
			return false
		}
	}
	if key := query.Get("key"); key != "" && key != "jpg" {
		return false
	}
	v := query.Get("v")
	return len(v) > 0 && len(v) <= 14 && numericID(v)
}

func (s *server) setApplyKey() ([]byte, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if len(s.artworkApplyKey) != sha256.Size {
		s.artworkApplyKey = make([]byte, sha256.Size)
		if _, err := rand.Read(s.artworkApplyKey); err != nil {
			s.artworkApplyKey = nil
			return nil, err
		}
	}
	return append([]byte(nil), s.artworkApplyKey...), nil
}

func (s *server) signSetApplyToken(claim setApplyToken) (string, error) {
	key, err := s.setApplyKey()
	if err != nil {
		return "", err
	}
	if claim.Nonce == "" {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		claim.Nonce = base64.RawURLEncoding.EncodeToString(raw)
	}
	payload, err := json.Marshal(claim)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (s *server) verifySetApplyToken(token string) (setApplyToken, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 || len(token) > 8192 {
		return setApplyToken{}, errors.New("invalid token")
	}
	key, err := s.setApplyKey()
	if err != nil {
		return setApplyToken{}, err
	}
	provided, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(provided) != parts[1] {
		return setApplyToken{}, errors.New("invalid token")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(parts[0]))
	if subtle.ConstantTimeCompare(provided, mac.Sum(nil)) != 1 {
		return setApplyToken{}, errors.New("invalid token")
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil || base64.RawURLEncoding.EncodeToString(payload) != parts[0] {
		return setApplyToken{}, errors.New("invalid token")
	}
	var claim setApplyToken
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if err := d.Decode(&claim); err != nil || requireEOF(d) != nil || claim.ExpiresAt <= time.Now().Unix() {
		return setApplyToken{}, errors.New("invalid token")
	}
	nonce, nonceErr := base64.RawURLEncoding.DecodeString(claim.Nonce)
	if claim.Role != "admin" || !validArtworkTargetID(claim.TargetID) || (claim.Type != "movie" && claim.Type != "series") || !numericID(claim.ID) || !validSetID(claim.SetID) || !assetID.MatchString(claim.AssetID) || nativeImageType(claim.Kind) != claim.ImageType || !fixedMediuxHTTPS(claim.SourceURL, claim.AssetID) || nonceErr != nil || len(nonce) != 16 || base64.RawURLEncoding.EncodeToString(nonce) != claim.Nonce {
		return setApplyToken{}, errors.New("invalid token")
	}
	return claim, nil
}

// consumeSetApplyToken makes each signed operation single-use before a native
// write begins. Failed and timed-out native calls intentionally remain consumed:
// the caller must refresh the artwork state before deciding what to do next.
func (s *server) consumeSetApplyToken(token string, expiresAt int64) (int, string) {
	digest := sha256.Sum256([]byte(token))
	now := time.Now().Unix()
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if s.artworkApplyUsed == nil {
		s.artworkApplyUsed = make(map[[32]byte]int64)
	}
	if _, used := s.artworkApplyUsed[digest]; used {
		return http.StatusConflict, "Artwork operation already attempted; review current artwork."
	}
	if len(s.artworkApplyUsed) >= 8192 {
		for previous, until := range s.artworkApplyUsed {
			if until <= now {
				delete(s.artworkApplyUsed, previous)
			}
		}
		if len(s.artworkApplyUsed) >= 8192 {
			return http.StatusTooManyRequests, "Artwork operations are busy; refresh and try again."
		}
	}
	s.artworkApplyUsed[digest] = expiresAt
	return 0, ""
}

func (s *server) applySetArtwork(ctx context.Context, raw []byte) (*pluginv1.HandleHTTPResponse, error) {
	var in setApplyInput
	if err := strictSetApplyJSON(raw, &in); err != nil {
		return settingsError(http.StatusBadRequest, "Invalid artwork apply request")
	}
	if !s.validSetApplyCSRF(in.CSRF) {
		return settingsError(http.StatusForbidden, "Invalid CSRF token")
	}
	claim, err := s.verifySetApplyToken(in.Token)
	if err != nil {
		return settingsError(http.StatusForbidden, "Invalid or expired artwork operation")
	}
	if status, message := s.consumeSetApplyToken(in.Token, claim.ExpiresAt); status != 0 {
		return settingsError(status, message)
	}
	payload, _ := json.Marshal(map[string]string{"original_url": claim.SourceURL, "type": claim.ImageType, "provider_id": "mediux"})
	native, _, _, err := s.collectionRequest(ctx, http.MethodPost, "/api/v2/admin/items/"+url.PathEscape(claim.TargetID)+"/images/apply", payload, "")
	if err != nil {
		return collectionHTTPError(err)
	}
	var result struct {
		Revision   any    `json:"revision"`
		ImageURL   string `json:"image_url"`
		StoredPath string `json:"stored_path"`
	}
	if json.Unmarshal(native, &result) != nil {
		return settingsError(http.StatusBadGateway, "Invalid artwork apply response")
	}
	body, _ := json.Marshal(map[string]any{"target_id": claim.TargetID, "kind": claim.Kind, "image_url": result.ImageURL, "revision": result.Revision, "stored_path": result.StoredPath})
	return settingsResponse(http.StatusOK, body, "application/json"), nil
}
