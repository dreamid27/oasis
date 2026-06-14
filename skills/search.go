package skills

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode"
)

// bm25Searcher ranks skills with Okapi BM25 over name+description+tags+instructions.
// It reads the underlying provider on every query (no caching) — always fresh, but
// O(skills) reads per query. For large corpora, implement SkillSearcher directly
// with a persistent or vector index.
type bm25Searcher struct {
	provider SkillProvider
}

// NewBM25Searcher returns a SkillSearcher backed by the given provider using BM25.
func NewBM25Searcher(p SkillProvider) SkillSearcher {
	return &bm25Searcher{provider: p}
}

// Compile-time check.
var _ SkillSearcher = (*bm25Searcher)(nil)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

func (s *bm25Searcher) SearchSkills(ctx context.Context, query string, limit int) ([]SkillSearchResult, error) {
	qTerms := tokenize(query)
	if len(qTerms) == 0 {
		return nil, nil
	}
	summaries, err := s.provider.Discover(ctx)
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, nil
	}

	type document struct {
		summary SkillSummary
		tf      map[string]int
		length  int
	}
	docs := make([]document, 0, len(summaries))
	df := make(map[string]int)
	totalLen := 0
	for _, sm := range summaries {
		text := sm.Name + " " + sm.Description + " " + strings.Join(sm.Tags, " ")
		if full, aerr := s.provider.Activate(ctx, sm.Name); aerr == nil {
			text += " " + full.Instructions
		}
		terms := tokenize(text)
		tf := make(map[string]int, len(terms))
		for _, t := range terms {
			tf[t]++
		}
		for t := range tf {
			df[t]++
		}
		docs = append(docs, document{summary: sm, tf: tf, length: len(terms)})
		totalLen += len(terms)
	}
	avgLen := float64(totalLen) / float64(len(docs))
	if avgLen == 0 {
		return nil, nil
	}
	n := float64(len(docs))

	results := make([]SkillSearchResult, 0, len(docs))
	for _, d := range docs {
		var score float64
		for _, qt := range qTerms {
			tf := float64(d.tf[qt])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df[qt])+0.5)/(float64(df[qt])+0.5))
			denom := tf + bm25K1*(1-bm25B+bm25B*float64(d.length)/avgLen)
			score += idf * (tf * (bm25K1 + 1)) / denom
		}
		if score > 0 {
			results = append(results, SkillSearchResult{SkillSummary: d.summary, Score: score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Name < results[j].Name
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// tokenize lowercases s and splits it on any non-alphanumeric rune.
func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}
