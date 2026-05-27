// memory/trim.go
package memory

import (
	"context"
	"sort"
	"unicode/utf8"

	"github.com/nevindra/oasis/core"
)

// estimateTokens returns a rough token count for a chat message.
// ~4 runes per token + small role-marker overhead.
func estimateTokens(msg core.ChatMessage) int {
	return utf8.RuneCountInString(msg.Content)/4 + 4
}

// trimHistoryOldestFirst drops oldest messages from messages[historyStart:historyEnd]
// until totalTokens <= budget. The leading system prompt (if any) is preserved.
func trimHistoryOldestFirst(messages []core.ChatMessage, historyStart, historyEnd, totalTokens, budget int) []core.ChatMessage {
	for totalTokens > budget && historyStart < historyEnd {
		totalTokens -= estimateTokens(messages[historyStart])
		historyStart++
	}
	trimmed := make([]core.ChatMessage, 0, len(messages))
	if len(messages) > 0 && messages[0].Role == "system" {
		trimmed = append(trimmed, messages[0])
	}
	trimmed = append(trimmed, messages[historyStart:historyEnd]...)
	return trimmed
}

// doSemanticTrim is the core semantic-trim algorithm. It drops messages with
// the lowest cosine similarity to inputEmbedding first, while preserving the
// most-recent keepRecent messages. Falls back to oldest-first on any
// embedding-pipeline failure. cache may be nil (no caching).
func doSemanticTrim(ctx context.Context, embedder core.EmbeddingProvider, cache *embeddingCache, messages []core.ChatMessage, historyStart, historyEnd, totalTokens, budget int, inputEmbedding []float32, keepRecent int) []core.ChatMessage {
	if embedder == nil || len(inputEmbedding) == 0 || historyEnd-historyStart <= keepRecent {
		return trimHistoryOldestFirst(messages, historyStart, historyEnd, totalTokens, budget)
	}
	olderEnd := historyEnd - keepRecent
	olderCount := olderEnd - historyStart
	olderEmbeddings := make([][]float32, olderCount)
	var missTexts []string
	var missSlots []int
	for i := 0; i < olderCount; i++ {
		text := messages[historyStart+i].Content
		if cache != nil {
			if cached, ok := cache.get(text); ok {
				olderEmbeddings[i] = cached
				continue
			}
		}
		missTexts = append(missTexts, text)
		missSlots = append(missSlots, i)
	}
	if len(missTexts) > 0 {
		embs, err := embedder.Embed(ctx, missTexts)
		if err != nil || len(embs) != len(missTexts) {
			return trimHistoryOldestFirst(messages, historyStart, historyEnd, totalTokens, budget)
		}
		for i, e := range embs {
			olderEmbeddings[missSlots[i]] = e
			if cache != nil {
				cache.put(missTexts[i], e)
			}
		}
	}
	type scored struct {
		idx   int
		score float32
	}
	items := make([]scored, olderCount)
	for i, e := range olderEmbeddings {
		items[i] = scored{idx: historyStart + i, score: core.CosineSimilarity(inputEmbedding, e)}
	}
	sort.Slice(items, func(a, b int) bool { return items[a].score < items[b].score })
	dropSet := make(map[int]bool)
	remaining := totalTokens
	for _, it := range items {
		if remaining <= budget {
			break
		}
		remaining -= estimateTokens(messages[it.idx])
		dropSet[it.idx] = true
	}
	out := make([]core.ChatMessage, 0, len(messages)-len(dropSet))
	for i, msg := range messages {
		if !dropSet[i] {
			out = append(out, msg)
		}
	}
	return out
}

// trimHistorySemantic drops messages with the lowest cosine similarity to
// inputEmbedding first, while preserving the most-recent keepRecent messages.
// Falls back to oldest-first on any embedding-pipeline failure.
func (m *AgentMemory) trimHistorySemantic(ctx context.Context, messages []core.ChatMessage, historyStart, historyEnd, totalTokens, budget int, inputEmbedding []float32, keepRecent int) []core.ChatMessage {
	m.initTrimCache()
	return doSemanticTrim(ctx, m.embedding, m.trimCache, messages, historyStart, historyEnd, totalTokens, budget, inputEmbedding, keepRecent)
}
