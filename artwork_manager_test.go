package main

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

func artworkRequest(method, path, role string) *pluginv1.HandleHTTPRequest {
	return &pluginv1.HandleHTTPRequest{Method: method, Path: path, Headers: map[string]string{"X-Silo-User-Role": role}}
}

func TestArtworkManagerAccessAndLegacyRoutes(t *testing.T) {
	s := &server{}
	for _, path := range []string{"/artwork", "/sets", "/collections"} {
		response, err := s.handleArtworkManager(context.Background(), artworkRequest("GET", path, "admin"))
		if err != nil || response.GetStatusCode() != 200 {
			t.Fatalf("%s response=%#v err=%v", path, response, err)
		}
		body := string(response.GetBody())
		if output := os.Getenv("ARTWORK_MANAGER_TEST_HTML"); output != "" && path == "/artwork" {
			if err := os.WriteFile(output, response.GetBody(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if !strings.Contains(body, "Artwork Manager") || !strings.Contains(body, "artwork:view") {
			t.Fatalf("%s did not render manager shell", path)
		}
	}
	for _, request := range []*pluginv1.HandleHTTPRequest{
		artworkRequest("GET", "/artwork", "user"), artworkRequest("POST", "/artwork", "admin"), artworkRequest("GET", "/missing", "admin"),
	} {
		response, err := s.handleArtworkManager(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if request.GetHeaders()["X-Silo-User-Role"] != "admin" && response.GetStatusCode() != 403 {
			t.Fatalf("non-admin response=%#v", response)
		}
		if request.GetMethod() != "GET" && response.GetStatusCode() != 405 {
			t.Fatalf("method response=%#v", response)
		}
		if request.GetPath() == "/missing" && response.GetStatusCode() != 404 {
			t.Fatalf("path response=%#v", response)
		}
	}
}

func TestArtworkManagerCompilesNamespacedComponents(t *testing.T) {
	body := string(renderArtworkManager())
	for _, want := range []string{
		`id="media-view"`, `id="collections-view"`, `id="media-type"`, `id="collections-collection-list"`,
		`id="tab-media"`, `role="tablist"`, `aria-controls="media-view"`, `new CustomEvent("artwork:view"`,
		`#media-view`, `#collections-view`, `<link rel="stylesheet" href="ui.css">`,
		`path.endsWith("/collections")`, `path.endsWith("/sets")`, `history.replaceState`, `detail:view`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q", want)
		}
	}
	if strings.Contains(body, `id="back"`) || strings.Contains(body, `id="type"`) || strings.Contains(body, `id="collection-list"`) {
		t.Fatalf("unprefixed component id in rendered page")
	}
	if strings.Count(body, `id="mediux-silo-theme"`) != 1 {
		t.Fatalf("theme bootstrap was not singular")
	}
	ids := regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(body, -1)
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id[1]] {
			t.Fatalf("duplicate id %q", id[1])
		}
		seen[id[1]] = true
	}
	if strings.Contains(body, "server-only-token") || strings.Contains(body, "api_token") {
		t.Fatalf("rendered HTML leaked a configuration secret")
	}
}

func TestArtworkComponentExtractionAndScopedCSS(t *testing.T) {
	page := []byte(`<!doctype html><html><head><style>body,.card{color:red}@media (max-width:1px){main,.x{display:block}}</style></head><body><main><header><h1>Old</h1></header><label for="name" aria-describedby="note"><input id="name"><span id="note"></span></label><script>const byId=id=>document.getElementById(id),x=byId("name"),y=byId("note");</script></main></body></html>`)
	component := extractArtworkComponent(page, "media-", "#media-view")
	for _, want := range []string{`id="media-name"`, `id="media-note"`, `for="media-name"`, `aria-describedby="media-note"`, `getElementById("media-"+id)`, `byId("note")`} {
		if !strings.Contains(component.content+component.script, want) {
			t.Fatalf("missing transformed %q", want)
		}
	}
	if strings.Contains(component.content, "Old") {
		t.Fatalf("source header was retained")
	}
	if !strings.Contains(component.style, "#media-view") || strings.Contains(component.style, "body,.card") {
		t.Fatalf("CSS was not scoped: %s", component.style)
	}
}
