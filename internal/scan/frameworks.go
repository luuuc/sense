package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var frameworkPatterns = map[string][]frameworkRule{
	"Gemfile": {
		{pattern: regexp.MustCompile(`gem\s+['"]rails['"]`), name: "Rails"},
		{pattern: regexp.MustCompile(`gem\s+['"]sinatra['"]`), name: "Sinatra"},
		{pattern: regexp.MustCompile(`gem\s+['"]hanami['"]`), name: "Hanami"},
	},
	"go.mod": {
		{pattern: regexp.MustCompile(`(?m)^.*github\.com/gin-gonic/gin\b`), name: "Gin"},
		{pattern: regexp.MustCompile(`(?m)^.*github\.com/labstack/echo\b`), name: "Echo"},
		{pattern: regexp.MustCompile(`(?m)^.*github\.com/gofiber/fiber\b`), name: "Fiber"},
	},
	"package.json": {
		{pattern: regexp.MustCompile(`"next"`), name: "Next.js"},
		{pattern: regexp.MustCompile(`"react"`), name: "React"},
		{pattern: regexp.MustCompile(`"vue"`), name: "Vue"},
		{pattern: regexp.MustCompile(`"angular"`), name: "Angular"},
		{pattern: regexp.MustCompile(`"express"`), name: "Express"},
	},
	"requirements.txt": {
		{pattern: regexp.MustCompile(`(?im)^django\b`), name: "Django"},
		{pattern: regexp.MustCompile(`(?im)^flask\b`), name: "Flask"},
		{pattern: regexp.MustCompile(`(?im)^fastapi\b`), name: "FastAPI"},
	},
	"pyproject.toml": {
		{pattern: regexp.MustCompile(`(?i)django`), name: "Django"},
		{pattern: regexp.MustCompile(`(?i)flask`), name: "Flask"},
		{pattern: regexp.MustCompile(`(?i)fastapi`), name: "FastAPI"},
	},
}

type frameworkRule struct {
	pattern *regexp.Regexp
	name    string
}

func detectFrameworks(root string) []string {
	seen := map[string]struct{}{}
	var result []string

	for filename, rules := range frameworkPatterns {
		data, err := os.ReadFile(filepath.Join(root, filename))
		if err != nil {
			continue
		}
		content := string(data)
		for _, rule := range rules {
			if rule.pattern.MatchString(content) {
				if _, ok := seen[rule.name]; !ok {
					seen[rule.name] = struct{}{}
					result = append(result, rule.name)
				}
			}
		}
	}

	sort.Strings(result)
	return result
}

func frameworksJSON(frameworks []string) string {
	if len(frameworks) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(frameworks)
	return string(b)
}
