package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/nevindra/oasis/skills"
)

type fakeProvider struct{ summaries []skills.SkillSummary }

func (f *fakeProvider) Discover(ctx context.Context) ([]skills.SkillSummary, error) {
	return f.summaries, nil
}
func (f *fakeProvider) Activate(ctx context.Context, name string) (skills.Skill, error) {
	return skills.Skill{Name: name}, nil
}

func TestBuildSkillCatalogContents(t *testing.T) {
	p := &fakeProvider{summaries: []skills.SkillSummary{
		{Name: "pdf", Description: "work with PDFs", Tags: []string{"docs"}},
		{Name: "chart", Description: "make charts"},
	}}
	block := buildSkillCatalog(context.Background(), p, nil)
	if !strings.Contains(block, "# Available Skills") {
		t.Fatalf("missing header: %q", block)
	}
	if !strings.Contains(block, "pdf — work with PDFs [docs]") {
		t.Fatalf("missing pdf entry: %q", block)
	}
	if !strings.Contains(block, "chart — make charts") {
		t.Fatalf("missing chart entry: %q", block)
	}
}

func TestBuildSkillCatalogExcludesActive(t *testing.T) {
	p := &fakeProvider{summaries: []skills.SkillSummary{
		{Name: "pdf", Description: "work with PDFs"},
		{Name: "chart", Description: "make charts"},
	}}
	block := buildSkillCatalog(context.Background(), p, []skills.Skill{{Name: "pdf"}})
	if strings.Contains(block, "pdf —") {
		t.Fatalf("active skill should be excluded: %q", block)
	}
	if !strings.Contains(block, "chart —") {
		t.Fatalf("non-active skill should appear: %q", block)
	}
}

func TestBuildSkillCatalogEmpty(t *testing.T) {
	block := buildSkillCatalog(context.Background(), &fakeProvider{}, nil)
	if block != "" {
		t.Fatalf("expected empty block for no skills, got %q", block)
	}
}
