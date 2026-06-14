package skills

import (
	"context"
	"strings"
	"testing"
)

// toolNames returns the Definition().Name of each tool from NewSkillTools.
func toolNames(provider SkillProvider) map[string]bool {
	names := map[string]bool{}
	for _, tl := range NewSkillTools(provider) {
		names[tl.Definition().Name] = true
	}
	return names
}

// plainProvider implements SkillProvider with no optional capabilities.
type plainProvider struct{}

func (plainProvider) Discover(ctx context.Context) ([]SkillSummary, error) {
	return []SkillSummary{{Name: "x", Description: "x skill"}}, nil
}

func (plainProvider) Activate(ctx context.Context, name string) (Skill, error) {
	if name == "x" {
		return Skill{Name: "x", Description: "x skill", Instructions: "do x"}, nil
	}
	return Skill{}, context.Canceled
}

// searchProvider implements SkillProvider + a custom SkillSearcher.
type searchProvider struct {
	plainProvider
	called *bool
}

func (s searchProvider) SearchSkills(ctx context.Context, query string, limit int) ([]SkillSearchResult, error) {
	*s.called = true
	return []SkillSearchResult{{SkillSummary: SkillSummary{Name: "custom"}, Score: 1}}, nil
}

func TestResourceToolsRegisteredWhenSupported(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "greeter", "hi", map[string]string{"refs/a.md": "AAA"})
	names := toolNames(FromDir(dir))
	for _, want := range []string{"skill_read", "skill_list_resources"} {
		if !names[want] {
			t.Errorf("expected tool %q to be registered", want)
		}
	}
}

func TestResourceToolsAbsentWhenUnsupported(t *testing.T) {
	names := toolNames(plainProvider{})
	if names["skill_read"] || names["skill_list_resources"] {
		t.Error("resource tools must not register for a provider without SkillResources")
	}
}

func TestSkillReadToolExecute(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "greeter", "hi", map[string]string{"refs/a.md": "AAA-CONTENT"})
	// Construct the tool directly from its dependency (avoids unwrapping AnyTool).
	rd := &skillReadTool{resources: FromDir(dir).(SkillResources)}
	out, err := rd.Execute(context.Background(), skillReadIn{Name: "greeter", Path: "refs/a.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "AAA-CONTENT") {
		t.Fatalf("got %q", out)
	}
}

func TestSkillReadToolTruncatesLargeFile(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("a", maxResourceBytes+5000)
	writeSkill(t, dir, "greeter", "hi", map[string]string{"big.txt": big})
	rd := &skillReadTool{resources: FromDir(dir).(SkillResources)}
	out, err := rd.Execute(context.Background(), skillReadIn{Name: "greeter", Path: "big.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[truncated:") {
		t.Fatalf("expected truncation notice, got %d bytes", len(out))
	}
	if len(out) > maxResourceBytes+100 {
		t.Fatalf("output not truncated: %d bytes", len(out))
	}
}

func TestSkillReadToolBinaryNotShown(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "greeter", "hi", map[string]string{"blob.bin": string([]byte{0x00, 0xff, 0xfe, 0x01, 0x02})})
	rd := &skillReadTool{resources: FromDir(dir).(SkillResources)}
	out, err := rd.Execute(context.Background(), skillReadIn{Name: "greeter", Path: "blob.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "binary file") {
		t.Fatalf("expected binary notice, got %q", out)
	}
}

func TestSearchToolAlwaysRegistered(t *testing.T) {
	if !toolNames(plainProvider{})["skill_search"] {
		t.Error("skill_search must be registered even for a plain provider (BM25 fallback)")
	}
}

func TestSearchToolUsesProviderSearcher(t *testing.T) {
	called := false
	p := searchProvider{called: &called}
	// Confirm registration, then exercise the wiring path directly.
	if !toolNames(p)["skill_search"] {
		t.Fatal("skill_search not registered")
	}
	st := &skillSearchTool{searcher: p}
	if _, err := st.Execute(context.Background(), skillSearchIn{Query: "anything"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("expected provider's SkillSearcher to be used")
	}
}
