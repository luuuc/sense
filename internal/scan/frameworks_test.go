package scan

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectFrameworksGemfile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Gemfile"), `
source "https://rubygems.org"
gem "rails", "~> 7.0"
gem "pg"
gem "puma"
`)

	fw := detectFrameworks(dir)
	if len(fw) != 1 || fw[0] != "Rails" {
		t.Errorf("expected [Rails], got %v", fw)
	}
}

func TestDetectFrameworksPackageJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{
  "dependencies": {
    "next": "13.0.0",
    "react": "18.0.0"
  }
}`)

	fw := detectFrameworks(dir)
	if len(fw) != 2 {
		t.Fatalf("expected 2 frameworks, got %v", fw)
	}
	has := map[string]bool{}
	for _, f := range fw {
		has[f] = true
	}
	if !has["Next.js"] || !has["React"] {
		t.Errorf("expected Next.js and React, got %v", fw)
	}
}

func TestDetectFrameworksGoMod(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), `
module example.com/myapp

go 1.21

require github.com/gin-gonic/gin v1.9.0
`)

	fw := detectFrameworks(dir)
	if len(fw) != 1 || fw[0] != "Gin" {
		t.Errorf("expected [Gin], got %v", fw)
	}
}

func TestDetectFrameworksRequirementsTxt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "requirements.txt"), `
Django>=4.0
djangorestframework
celery
`)

	fw := detectFrameworks(dir)
	if len(fw) != 1 || fw[0] != "Django" {
		t.Errorf("expected [Django], got %v", fw)
	}
}

func TestDetectFrameworksPyprojectToml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pyproject.toml"), `
[project]
dependencies = ["fastapi>=0.100", "uvicorn"]
`)

	fw := detectFrameworks(dir)
	if len(fw) != 1 || fw[0] != "FastAPI" {
		t.Errorf("expected [FastAPI], got %v", fw)
	}
}

func TestDetectFrameworksNoDependencyFiles(t *testing.T) {
	dir := t.TempDir()
	fw := detectFrameworks(dir)
	if len(fw) != 0 {
		t.Errorf("expected empty, got %v", fw)
	}
}

func TestDetectFrameworksNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "requirements.txt"), "django\n")
	writeFile(t, filepath.Join(dir, "pyproject.toml"), "dependencies = [\"django\"]\n")

	fw := detectFrameworks(dir)
	count := 0
	for _, f := range fw {
		if f == "Django" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected Django once, got %v", fw)
	}
}

func TestFrameworksJSON(t *testing.T) {
	got := frameworksJSON([]string{"Rails", "React"})
	want := `["Rails","React"]`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	got = frameworksJSON(nil)
	if got != "[]" {
		t.Errorf("got %q, want %q", got, "[]")
	}
}
