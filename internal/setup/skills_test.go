package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSkillsCreatesAllFiles(t *testing.T) {
	root := t.TempDir()

	n, err := writeSkills(root)
	if err != nil {
		t.Fatalf("writeSkills: %v", err)
	}
	if n != len(skills) {
		t.Fatalf("writeSkills returned %d, want %d", n, len(skills))
	}

	for _, s := range skills {
		path := filepath.Join(root, ".claude", "skills", s.filename)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("ReadFile %s: %v", path, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", path)
		}
	}
}

func TestWriteSkillsMkdirFails(t *testing.T) {
	root := t.TempDir()

	// Pre-create .claude as a regular file so MkdirAll on .claude/skills fails.
	if err := os.WriteFile(filepath.Join(root, ".claude"), []byte("blocker"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	n, err := writeSkills(root)
	if err == nil {
		t.Fatalf("writeSkills succeeded, want error")
	}
	if n != 0 {
		t.Errorf("writeSkills returned %d on mkdir fail, want 0", n)
	}
}

func TestWriteSkillsWriteFails(t *testing.T) {
	root := t.TempDir()

	// Pre-create the first skill's path as a directory so WriteFile errors on it.
	skillsDir := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(filepath.Join(skillsDir, skills[0].filename), 0o755); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	n, err := writeSkills(root)
	if err == nil {
		t.Fatalf("writeSkills succeeded, want error")
	}
	if n != 0 {
		t.Errorf("writeSkills returned %d on first-write fail, want 0", n)
	}
}
