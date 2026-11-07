package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

//go:embed settings.html
var settingsPage []byte

const maxSettingsBody = 16 << 10

func settingsResponse(status int, body []byte, contentType string) *pluginv1.HandleHTTPResponse {
	return &pluginv1.HandleHTTPResponse{StatusCode: int32(status), Body: body, Headers: map[string]string{"Content-Type": contentType, "Cache-Control": "no-store"}}
}

func settingsError(status int, message string) (*pluginv1.HandleHTTPResponse, error) {
	body, err := json.Marshal(map[string]string{"error": message})
	if err != nil {
		return nil, err
	}
	return settingsResponse(status, body, "application/json"), nil
}

func (s *server) settingsCSRFLocked() (string, error) {
	if s.settingsCSRF != "" {
		return s.settingsCSRF, nil
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	s.settingsCSRF = hex.EncodeToString(raw)
	return s.settingsCSRF, nil
}

func publicPreferences(cfg settings) map[string]any {
	return map[string]any{
		"priority_creators":      cfg.Priority,
		"excluded_creators":      cfg.Excluded,
		"only_priority_creators": cfg.OnlyPriority,
		"season_consistency":     cfg.SeasonConsistency,
		"titlecard_consistency":  cfg.TitlecardConsistency,
		"missing_artwork":        cfg.MissingArtwork,
		"image_selection":        cfg.Selection,
		"quality":                cfg.Quality,
		"cache_hours":            cfg.CacheHours,
	}
}

func (s *server) settingsConfig() (map[string]any, error) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	csrf, err := s.settingsCSRFLocked()
	if err != nil {
		return nil, err
	}
	cfg := s.p.snapshot()
	public := publicPreferences(cfg)
	public["csrf"] = csrf
	public["token_configured"] = strings.TrimSpace(cfg.Token) != ""
	public["collection_connection_configured"] = strings.TrimSpace(s.collectionKey) != ""
	return public, nil
}

func normalizeCreatorCSV(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n") {
		return "", errors.New("creator names cannot contain newlines")
	}
	seen := map[string]bool{}
	names := make([]string, 0)
	for _, name := range strings.Split(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if utf8.RuneCountInString(name) > 128 {
			return "", errors.New("creator names must be at most 128 characters")
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		if len(names) == 100 {
			return "", errors.New("at most 100 creators are allowed")
		}
		seen[key] = true
		names = append(names, name)
	}
	return strings.Join(names, ","), nil
}

func validChoice(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func mergePreferences(current map[string]any, raw map[string]json.RawMessage) (map[string]any, error) {
	next := make(map[string]any, len(current))
	for key, value := range current {
		next[key] = value
	}
	for key, data := range raw {
		if key == "api_token" {
			return nil, errors.New("api_token cannot be changed here")
		}
		switch key {
		case "priority_creators", "excluded_creators":
			var value string
			if err := json.Unmarshal(data, &value); err != nil {
				return nil, errors.New(key + " must be a string")
			}
			value, err := normalizeCreatorCSV(value)
			if err != nil {
				return nil, err
			}
			next[key] = value
		case "only_priority_creators":
			var value bool
			if err := json.Unmarshal(data, &value); err != nil {
				return nil, errors.New("only_priority_creators must be a boolean")
			}
			next[key] = value
		case "season_consistency", "titlecard_consistency":
			var value string
			if err := json.Unmarshal(data, &value); err != nil || !validChoice(value, "MostComplete", "CreatorPriority", "SeriesPoster", "Independent") {
				return nil, errors.New(key + " is invalid")
			}
			next[key] = value
		case "missing_artwork":
			var value string
			if err := json.Unmarshal(data, &value); err != nil || !validChoice(value, "Fallback", "KeepExisting") {
				return nil, errors.New("missing_artwork is invalid")
			}
			next[key] = value
		case "image_selection":
			var value string
			if err := json.Unmarshal(data, &value); err != nil || !validChoice(value, "BestOnly", "All") {
				return nil, errors.New("image_selection is invalid")
			}
			next[key] = value
		case "quality":
			var value string
			if err := json.Unmarshal(data, &value); err != nil || !validChoice(value, "optimized", "original") {
				return nil, errors.New("quality is invalid")
			}
			next[key] = value
		case "cache_hours":
			var value int
			if err := json.Unmarshal(data, &value); err != nil || value < 0 || value > 168 {
				return nil, errors.New("cache_hours must be an integer from 0 through 168")
			}
			next[key] = value
		default:
			return nil, errors.New("unknown settings field")
		}
	}
	return next, nil
}

func decodePreferencePatch(body []byte) (map[string]json.RawMessage, error) {
	if len(body) > maxSettingsBody {
		return nil, errors.New("settings payload is too large")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil || raw == nil {
		return nil, errors.New("settings payload must be a JSON object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("settings payload must contain one JSON object")
	}
	return raw, nil
}

func (s *server) saveSettings(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	if len(r.GetBody()) > maxSettingsBody {
		return settingsError(413, "Settings payload is too large")
	}
	patch, err := decodePreferencePatch(r.GetBody())
	if err != nil {
		return settingsError(400, err.Error())
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	csrf, err := s.settingsCSRFLocked()
	if err != nil {
		return settingsError(500, "Could not prepare settings")
	}
	csrfBody, exists := patch["csrf"]
	if !exists {
		return settingsError(403, "Invalid CSRF token")
	}
	var receivedCSRF string
	if err := json.Unmarshal(csrfBody, &receivedCSRF); err != nil || subtle.ConstantTimeCompare([]byte(receivedCSRF), []byte(csrf)) != 1 {
		return settingsError(403, "Invalid CSRF token")
	}
	delete(patch, "csrf")
	preferences, err := mergePreferences(publicPreferences(s.p.snapshot()), patch)
	if err != nil {
		return settingsError(400, err.Error())
	}
	if s.savePreferences == nil {
		return settingsError(500, "Settings persistence is unavailable")
	}
	if err := s.savePreferences(ctx, preferences); err != nil {
		return settingsError(500, "Could not save settings")
	}
	s.p.configure(preferences)
	response := make(map[string]any, len(preferences)+2)
	for key, value := range preferences {
		response[key] = value
	}
	response["csrf"] = csrf
	response["token_configured"] = strings.TrimSpace(s.p.snapshot().Token) != ""
	body, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return settingsResponse(200, body, "application/json"), nil
}

func (s *server) handleSettings(ctx context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	if r.GetHeaders()["X-Silo-User-Role"] != "admin" {
		return settingsError(403, "Administrator access required")
	}
	switch {
	case r.GetMethod() == "GET" && r.GetPath() == "/settings":
		return settingsResponse(200, settingsPage, "text/html; charset=utf-8"), nil
	case r.GetMethod() == "GET" && r.GetPath() == "/settings/config":
		body, err := s.settingsConfig()
		if err != nil {
			return settingsError(500, "Could not prepare settings")
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return settingsResponse(200, raw, "application/json"), nil
	case r.GetMethod() == "PUT" && r.GetPath() == "/settings/config":
		return s.saveSettings(ctx, r)
	default:
		return settingsError(404, "Not found")
	}
}
