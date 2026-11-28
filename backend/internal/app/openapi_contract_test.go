package app_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// TestOpenAPIContract fails when a registered route is missing from
// docs/api/openapi.yaml, when the spec documents a route that does not exist,
// or when a route in the API reference is not implemented.
func TestOpenAPIContract(t *testing.T) {
	e := newEnv(t, defaultNow)
	registered := map[string]bool{}
	for _, r := range e.app.Fiber.GetRoutes(true) {
		if !strings.HasPrefix(r.Path, "/api/v1/") || r.Method == "HEAD" || r.Method == "OPTIONS" {
			continue
		}
		registered[r.Method+" "+toOpenAPI(strings.TrimPrefix(r.Path, "/api/v1"))] = true
	}

	raw, err := os.ReadFile("../../../docs/api/openapi.yaml")
	require.NoError(t, err, "build it with: go run ./cmd/openapi")
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &spec))
	documented := map[string]bool{}
	for p, ops := range spec.Paths {
		for m := range ops {
			switch m {
			case "get", "post", "put", "delete", "patch":
				documented[strings.ToUpper(m)+" "+p] = true
			}
		}
	}
	require.Empty(t, diff(registered, documented), "routes missing from docs/api/openapi.yaml")
	require.Empty(t, diff(documented, registered), "documented in openapi.yaml but not registered")

	reference := apiReferenceRoutes(t)
	require.Greater(t, len(reference), 250)
	require.Empty(t, diff(reference, registered), "routes in docs/spec/API reference.md that are not implemented")
}

var paramRe = regexp.MustCompile(`:([a-z_]+)`)

func toOpenAPI(p string) string { return paramRe.ReplaceAllString(p, "{$1}") }

func diff(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// apiReferenceRoutes parses the method and path cells of the API reference tables.
// Cells look like "GET `/x`", "GET, POST `/x`; PUT `/x/:id`" or "POST `/x/:id/a`, `/b`",
// where a bare second path is a sibling action of the first.
func apiReferenceRoutes(t *testing.T) map[string]bool {
	raw, err := os.ReadFile("../../../docs/spec/API reference.md")
	require.NoError(t, err)
	seg := regexp.MustCompile(`^((?:GET|POST|PUT|PATCH|DELETE)(?:,\s*(?:GET|POST|PUT|PATCH|DELETE))*)\s+(.*)$`)
	tick := regexp.MustCompile("`([^`]+)`")
	out := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "| ") || !strings.Contains(line, "`/") {
			continue
		}
		cell := strings.TrimSpace(strings.Split(line, "|")[1])
		for _, part := range strings.Split(cell, ";") {
			m := seg.FindStringSubmatch(strings.TrimSpace(part))
			if m == nil {
				continue
			}
			var verbs []string
			for _, v := range strings.Split(m[1], ",") {
				verbs = append(verbs, strings.TrimSpace(v))
			}
			var first string
			for i, pm := range tick.FindAllStringSubmatch(m[2], -1) {
				p := strings.SplitN(pm[1], "?", 2)[0]
				if i == 0 {
					first = p
				} else if !strings.HasPrefix(p, "/"+strings.Split(first, "/")[1]) {
					p = first[:strings.LastIndex(first, "/")] + p
				}
				for _, v := range verbs {
					out[v+" "+toOpenAPI(p)] = true
				}
			}
		}
	}
	return out
}
