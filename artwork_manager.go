package main

import (
	"context"
	_ "embed"
	"regexp"
	"strings"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"
)

//go:embed artwork.html
var artworkTemplate string

var (
	bodyRE          = regexp.MustCompile(`(?is)<body[^>]*>(.*)</body>`)
	mainRE          = regexp.MustCompile(`(?is)<main[^>]*>(.*)</main>`)
	headerRE        = regexp.MustCompile(`(?is)^\s*<header[^>]*>.*?</header>`)
	styleRE         = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
	headRE          = regexp.MustCompile(`(?is)<head[^>]*>(.*?)</head>`)
	scriptRE        = regexp.MustCompile(`(?is)<script([^>]*)>(.*?)</script>`)
	idRE            = regexp.MustCompile(`\bid="([A-Za-z][A-Za-z0-9_-]*)"`)
	refRE           = regexp.MustCompile(`\b(for|aria-labelledby|aria-controls|aria-describedby)="([A-Za-z][A-Za-z0-9_-]*(?:\s+[A-Za-z][A-Za-z0-9_-]*)*)"`)
	dynamicGetterRE = regexp.MustCompile(`document\.getElementById\(id\)`)
)

type artworkComponent struct{ content, style, script string }

func extractArtworkComponent(page []byte, prefix, root string) artworkComponent {
	raw := string(page)
	body := raw
	if match := bodyRE.FindStringSubmatch(raw); len(match) == 2 {
		body = match[1]
	}
	if match := mainRE.FindStringSubmatch(body); len(match) == 2 {
		body = match[1]
	}
	body = headerRE.ReplaceAllString(body, "")
	body = scriptRE.ReplaceAllString(body, "")
	style := ""
	if match := styleRE.FindStringSubmatch(raw); len(match) == 2 {
		style = scopeArtworkCSS(match[1], root)
	}
	script := ""
	for _, match := range scriptRE.FindAllStringSubmatch(bodyRE.FindString(raw), -1) {
		if len(match) == 3 {
			script = match[2]
		}
	}
	body, script = prefixArtworkIDs(body, script, prefix)
	return artworkComponent{content: body, style: style, script: script}
}

func prefixArtworkIDs(content, script, prefix string) (string, string) {
	prefixID := func(id string) string { return prefix + id }
	content = idRE.ReplaceAllStringFunc(content, func(match string) string {
		id := idRE.FindStringSubmatch(match)[1]
		return `id="` + prefixID(id) + `"`
	})
	content = refRE.ReplaceAllStringFunc(content, func(match string) string {
		parts := refRE.FindStringSubmatch(match)
		ids := strings.Fields(parts[2])
		for i := range ids {
			ids[i] = prefixID(ids[i])
		}
		return parts[1] + `="` + strings.Join(ids, " ") + `"`
	})
	// Components use a dynamic helper (document.getElementById(id)). Prefixing
	// that helper keeps all of its static callers local, without changing shell
	// IDs used by the shared preview behavior.
	script = dynamicGetterRE.ReplaceAllString(script, `document.getElementById("`+prefix+`"+id)`)
	return content, script
}

// scopeArtworkCSS limits the embedded component styles to its tab. It handles
// nested media/supports blocks and intentionally leaves keyframes unchanged.
func scopeArtworkCSS(css, root string) string {
	var out strings.Builder
	for len(css) > 0 {
		open := strings.IndexByte(css, '{')
		if open < 0 {
			out.WriteString(css)
			break
		}
		selector := css[:open]
		depth, end := 1, open+1
		for ; end < len(css) && depth > 0; end++ {
			if css[end] == '{' {
				depth++
			}
			if css[end] == '}' {
				depth--
			}
		}
		if depth != 0 {
			out.WriteString(css)
			break
		}
		inner := css[open+1 : end-1]
		trimmed := strings.TrimSpace(selector)
		out.WriteString(scopeArtworkSelector(trimmed, root))
		out.WriteByte('{')
		if strings.HasPrefix(trimmed, "@media") || strings.HasPrefix(trimmed, "@supports") || strings.HasPrefix(trimmed, "@container") || strings.HasPrefix(trimmed, "@layer") {
			out.WriteString(scopeArtworkCSS(inner, root))
		} else {
			out.WriteString(inner)
		}
		out.WriteByte('}')
		css = css[end:]
	}
	return out.String()
}

func scopeArtworkSelector(selector, root string) string {
	if selector == "" || strings.HasPrefix(selector, "@") {
		return selector
	}
	items := strings.Split(selector, ",")
	for i, item := range items {
		item = strings.TrimSpace(item)
		if !strings.HasPrefix(item, root) {
			item = root + " " + item
		}
		items[i] = item
	}
	return strings.Join(items, ",")
}

func artworkThemeBootstrap(page []byte) string {
	head := ""
	if match := headRE.FindStringSubmatch(string(page)); len(match) == 2 {
		head = match[1]
	}
	for _, match := range scriptRE.FindAllStringSubmatch(head, -1) {
		if len(match) == 3 {
			return "<script" + match[1] + ">" + match[2] + "</script>"
		}
	}
	return ""
}

func renderArtworkManager() []byte {
	media := extractArtworkComponent(setsPage, "media-", "#media-view")
	collections := extractArtworkComponent(collectionsPage, "collections-", "#collections-view")
	page := strings.NewReplacer(
		"{{THEME}}", artworkThemeBootstrap(setsPage),
		"{{MEDIA_STYLE}}", media.style, "{{COLLECTIONS_STYLE}}", collections.style,
		"{{MEDIA_CONTENT}}", media.content, "{{COLLECTIONS_CONTENT}}", collections.content,
		"{{MEDIA_SCRIPT}}", "<script>"+media.script+"</script>",
		"{{COLLECTIONS_SCRIPT}}", "<script>"+collections.script+"</script>",
	).Replace(artworkTemplate)
	return []byte(page)
}

func (s *server) handleArtworkManager(_ context.Context, r *pluginv1.HandleHTTPRequest) (*pluginv1.HandleHTTPResponse, error) {
	if r.GetHeaders()["X-Silo-User-Role"] != "admin" {
		return settingsError(403, "Administrator access required")
	}
	if r.GetMethod() != "GET" {
		return settingsError(405, "Method not allowed")
	}
	if path := r.GetPath(); path != "/artwork" && path != "/sets" && path != "/collections" {
		return settingsError(404, "Not found")
	}
	body := renderArtworkManager()
	return settingsResponse(200, body, "text/html; charset=utf-8"), nil
}
