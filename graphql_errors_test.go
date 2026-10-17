package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func TestGraphQLErrorDetailsReachBothLookupPaths(t *testing.T) {
	for _, typ := range []string{"movie", "series"} {
		for _, lookup := range []bool{false, true} {
			t.Run(typ+map[bool]string{true: " identity", false: " artwork"}[lookup], func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"errors":[{"message":"Cannot query field collection_set","path":["movies_by_id","posters",0],"extensions":{"code":"GRAPHQL_VALIDATION_FAILED","stack":"private upstream stack"}}]}`))
				}))
				defer upstream.Close()
				p := newProvider()
				p.endpoint = upstream.URL
				p.configure(map[string]any{"api_token": "secret-token"})
				var err error
				s := &server{p: p}
				if lookup {
					_, err = s.GetMetadata(context.Background(), &pluginv1.GetMetadataRequest{ItemType: typ, ProviderIds: structFromMap(map[string]any{"tvdb": "123"})})
				} else {
					_, err = s.GetImages(context.Background(), &pluginv1.GetImagesRequest{ItemType: typ, ProviderId: typ + ":123"})
				}
				if err == nil {
					t.Fatal("GraphQL error accepted")
				}
				operation := typ + " artwork"
				if lookup {
					operation = "TVDB " + typ + " lookup"
				}
				for _, want := range []string{operation, "GRAPHQL_VALIDATION_FAILED", "Cannot query field collection_set", "movies_by_id.posters.0"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("missing %q: %v", want, err)
					}
				}
				if strings.Contains(err.Error(), "private upstream stack") {
					t.Fatal("upstream stack exposed")
				}
			})
		}
	}
}

func TestGraphQLErrorRedactsEveryExposedField(t *testing.T) {
	const token = "only-this-secret"
	for _, field := range []string{"message", "code", "path"} {
		t.Run(field, func(t *testing.T) {
			item := map[string]any{"message": "Permission denied"}
			switch field {
			case "message":
				item["message"] = "Permission denied for " + token
			case "code":
				item["extensions"] = map[string]any{"code": token}
			case "path":
				item["path"] = []string{"movies_by_id", token}
			}
			raw, _ := json.Marshal(item)
			got := graphqlError([]json.RawMessage{raw}, "movie artwork", token).Error()
			if strings.Contains(got, token) || !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "Permission denied") {
				t.Fatalf("unsafe or unhelpful diagnostic: %q", got)
			}
		})
	}
}

func TestGraphQLErrorRedactsBeforeTruncating(t *testing.T) {
	const token = "private/token+value?"
	entry, _ := json.Marshal(map[string]any{
		"message":    "Token " + token + " encoded private%2Ftoken%2Bvalue%3F Bearer another-secret\n\x1b[31m\u202eevil " + strings.Repeat("x", 600),
		"extensions": map[string]any{"code": token, "stacktrace": []string{"hidden"}},
		"path":       []any{"movies", token, 0},
	})
	got := graphqlError([]json.RawMessage{entry, entry, entry, entry}, "movie artwork", token).Error()
	for _, secret := range []string{token, "private%2Ftoken%2Bvalue%3F", "another-secret", "hidden", "\n", "\x1b", "\u202e"} {
		if strings.Contains(got, secret) {
			t.Fatalf("unsafe diagnostic contains %q", secret)
		}
	}
	if len(got) > 1800 || !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "1 additional errors omitted") {
		t.Fatalf("unbounded or unhelpful diagnostic: length=%d", len(got))
	}
}

func TestGraphQLErrorMalformedDetailsHaveSafeFallback(t *testing.T) {
	for _, entry := range []string{`null`, `{}`, `"bad"`, `{"message":{}}`, `{"extensions":{"code":12}}`} {
		got := graphqlError([]json.RawMessage{json.RawMessage(entry)}, "series artwork", "secret").Error()
		if got != "Mediux API returned GraphQL errors (series artwork)" {
			t.Fatalf("fallback=%q", got)
		}
	}
}

func TestGraphQLErrorRedactsEncodedTokensAndQuotedBearer(t *testing.T) {
	for _, secret := range []string{
		"private%2Ftoken%2Bvalue%3F",
		"private%2ftoken%2bvalue%3f",
		"private%2ftoken%2Bvalue%3f",
		`Bearer "another-secret"`,
		`Bearer 'another-secret'`,
	} {
		raw, _ := json.Marshal(map[string]any{"message": "Permission denied: " + secret})
		got := graphqlError([]json.RawMessage{raw}, "movie artwork", "private/token+value?").Error()
		if strings.Contains(got, "private") || strings.Contains(got, "another-secret") || !strings.Contains(got, "[REDACTED]") {
			t.Fatalf("credential exposed for %q: %q", secret, got)
		}
	}
}
