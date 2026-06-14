package skills

import (
	"context"
	"testing"
)

// memProvider serves skills from an in-memory map for deterministic search tests.
type memProvider struct{ skills map[string]Skill }

func (m memProvider) Discover(ctx context.Context) ([]SkillSummary, error) {
	var out []SkillSummary
	for _, s := range m.skills {
		out = append(out, SkillSummary{Name: s.Name, Description: s.Description, Tags: s.Tags})
	}
	return out, nil
}

func (m memProvider) Activate(ctx context.Context, name string) (Skill, error) {
	if s, ok := m.skills[name]; ok {
		return s, nil
	}
	return Skill{}, context.Canceled
}

func TestBM25RanksBodyMatch(t *testing.T) {
	p := memProvider{skills: map[string]Skill{
		"pdf":   {Name: "pdf", Description: "document tools", Instructions: "extract tables from invoices and receipts"},
		"chart": {Name: "chart", Description: "plotting", Instructions: "draw bar and line charts"},
	}}
	s := NewBM25Searcher(p)
	res, err := s.SearchSkills(context.Background(), "invoice tables", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 || res[0].Name != "pdf" {
		t.Fatalf("expected pdf first, got %+v", res)
	}
}

func TestBM25Limit(t *testing.T) {
	p := memProvider{skills: map[string]Skill{
		"a": {Name: "a", Description: "alpha report data"},
		"b": {Name: "b", Description: "beta report data"},
		"c": {Name: "c", Description: "gamma report data"},
	}}
	res, err := NewBM25Searcher(p).SearchSkills(context.Background(), "report", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
}

func TestBM25EmptyQueryAndCorpus(t *testing.T) {
	res, err := NewBM25Searcher(memProvider{skills: map[string]Skill{}}).SearchSkills(context.Background(), "x", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("expected 0 results for empty corpus, got %d", len(res))
	}
	res, err = NewBM25Searcher(memProvider{skills: map[string]Skill{"a": {Name: "a", Description: "x"}}}).SearchSkills(context.Background(), "   ", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 0 {
		t.Fatalf("expected 0 results for empty query, got %d", len(res))
	}
}
