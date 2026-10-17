package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var bearerCredential = regexp.MustCompile(`(?i)\bBearer\s+(?:"[^"]*"|'[^']*'|[^\s"'<>,;]+)`)

// Keep actionable upstream diagnostics, never arbitrary extension data or stacks.
func graphqlError(raw []json.RawMessage, operation, token string) error {
	clean := func(value string) string {
		if token != "" {
			value = strings.ReplaceAll(value, token, "[REDACTED]")
			for _, secret := range []string{url.QueryEscape(token), url.PathEscape(token)} {
				if secret != token {
					// Hex digits in percent escapes may use either case.
					encoded := regexp.MustCompile("(?i)" + regexp.QuoteMeta(secret))
					value = encoded.ReplaceAllLiteralString(value, "[REDACTED]")
				}
			}
		}
		value = bearerCredential.ReplaceAllString(value, "Bearer [REDACTED]")
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return ' '
			}
			return r
		}, value)
		value = strings.Join(strings.Fields(value), " ")
		if runes := []rune(value); len(runes) > 512 {
			value = string(runes[:512]) + "…"
		}
		return value
	}
	var details []string
	for _, entry := range raw[:min(len(raw), 3)] {
		var item struct {
			Message    string            `json:"message"`
			Path       []json.RawMessage `json:"path"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		}
		if json.Unmarshal(entry, &item) != nil {
			continue
		}
		detail := clean(item.Message)
		if code := clean(item.Extensions.Code); code != "" {
			detail = code + ": " + detail
		}
		var path []string
		for _, part := range item.Path[:min(len(item.Path), 8)] {
			var field string
			var index uint64
			if json.Unmarshal(part, &field) == nil {
				path = append(path, clean(field))
			} else if json.Unmarshal(part, &index) == nil {
				path = append(path, fmt.Sprint(index))
			}
		}
		if len(path) > 0 {
			detail += " [path: " + clean(strings.Join(path, ".")) + "]"
		}
		if strings.TrimSpace(detail) != "" {
			details = append(details, clean(detail))
		}
	}
	message := "Mediux API returned GraphQL errors (" + operation + ")"
	if len(details) > 0 {
		message += ": " + strings.Join(details, "; ")
	}
	if len(raw) > 3 {
		message += fmt.Sprintf("; %d additional errors omitted", len(raw)-3)
	}
	return fmt.Errorf("%s", message)
}
