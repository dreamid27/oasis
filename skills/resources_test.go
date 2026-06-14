package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeSkill creates <dir>/<name>/SKILL.md plus any extra files (relpath->content).
func writeSkill(t *testing.T, dir, name, body string, extra map[string]string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: test skill\n---\n" + body
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, content := range extra {
		p := filepath.Join(skillDir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFileProviderListResources(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "greeter", "hi", map[string]string{
		"references/api.md": "# API",
		"scripts/run.sh":    "echo hi",
	})
	p := FromDir(dir)
	r, ok := p.(SkillResources)
	if !ok {
		t.Fatal("FromDir provider does not implement SkillResources")
	}
	files, err := r.ListResources(context.Background(), "greeter")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"references/api.md", "scripts/run.sh"}
	if len(files) != len(want) {
		t.Fatalf("got %v, want %v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Fatalf("got %v, want %v", files, want)
		}
	}
}

func TestFileProviderReadResource(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "greeter", "hi", map[string]string{"references/api.md": "# API DOC"})
	r := FromDir(dir).(SkillResources)
	data, err := r.ReadResource(context.Background(), "greeter", "references/api.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# API DOC" {
		t.Fatalf("got %q", string(data))
	}
}

func TestFileProviderReadResourceTraversalRejected(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "greeter", "hi", nil)
	// Secret file outside the skill dir.
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("s3cr3t"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := FromDir(dir).(SkillResources)
	for _, bad := range []string{"../secret.txt", "/etc/passwd", "../../etc/passwd"} {
		if _, err := r.ReadResource(context.Background(), "greeter", bad); err == nil {
			t.Fatalf("expected error for path %q, got nil", bad)
		}
	}
}

func TestFileProviderReadResourceSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "greeter", "hi", nil)
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("s3cr3t"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink inside the skill dir pointing outside it must not be followed.
	link := filepath.Join(dir, "greeter", "leak")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks not supported on this platform: %v", err)
	}
	r := FromDir(dir).(SkillResources)
	if _, err := r.ReadResource(context.Background(), "greeter", "leak"); err == nil {
		t.Fatal("expected symlink escaping the skill directory to be rejected")
	}
}

func TestFileProviderResourcesMissingSkill(t *testing.T) {
	r := FromDir(t.TempDir()).(SkillResources)
	if _, err := r.ListResources(context.Background(), "nope"); err == nil {
		t.Fatal("expected error for missing skill")
	}
}

func TestChainedProviderRoutesResources(t *testing.T) {
	// dirA has no matching skill; dirB owns "greeter" with a companion file.
	// The chained provider must route resource calls to dirB.
	dirA := t.TempDir()
	writeSkill(t, dirA, "other", "x", nil)
	dirB := t.TempDir()
	writeSkill(t, dirB, "greeter", "hi", map[string]string{"notes.txt": "hello"})

	chained := Chain(FromDir(dirA), FromDir(dirB))
	r, ok := chained.(SkillResources)
	if !ok {
		t.Fatal("chained provider does not implement SkillResources")
	}
	files, err := r.ListResources(context.Background(), "greeter")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != "notes.txt" {
		t.Fatalf("got %v", files)
	}
	data, err := r.ReadResource(context.Background(), "greeter", "notes.txt")
	if err != nil || string(data) != "hello" {
		t.Fatalf("data=%q err=%v", string(data), err)
	}
}
