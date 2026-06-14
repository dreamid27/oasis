# Skills Examples

---

## Recipe 1: Give an agent access to all skills

**Goal:** Wire up a skill provider so the agent can discover and activate skills on its own.

```go
package main

import (
    "context"
    "fmt"

    "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/provider/openaicompat"
    "github.com/nevindra/oasis/skills"
)

func main() {
    // Load the application's skills from disk. Project skills take
    // precedence over user-level skills on name collisions.
    provider := skills.Chain(
        skills.FromDir("./skills"),
        skills.FromDir(skills.DefaultSkillDirs()...),
    )

    llm := openaicompat.New("https://api.openai.com/v1", "gpt-4o", "YOUR_KEY")

    ag := oasis.NewAgent(llm,
        oasis.WithPrompt("You are a helpful assistant. "+
            "When the task requires specialized knowledge, "+
            "call skill_discover to see available skills, then activate the right one."),
        oasis.WithSkills(provider),
    )

    result, err := ag.Execute(context.Background(), oasis.AgentTask{
        Input: "Create an Excel report from the attached CSV data.",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Output)
}
```

**Plain-English walkthrough:**

- `skills.Chain(...)` creates a merged provider. The first provider is searched first and wins on name collisions.
- `oasis.WithSkills(provider)` registers the skill tools automatically — `skill_discover`, `skill_activate`, and `skill_search` always, plus `skill_create`/`skill_update` (when the provider implements `SkillWriter`) and `skill_read`/`skill_list_resources` (when it implements `SkillResources`).
- The agent's prompt instructs it to use skills. The LLM decides which skill to activate based on the task.
- For the Excel task, the agent will activate whichever spreadsheet skill your application placed in `./skills`.

**Variations:**

- Implement `skills.SkillProvider` yourself to load skills from a database, object store, or remote service instead of disk.
- Pass multiple directories to `FromDir` for project-level + shared team skills: `skills.FromDir("./skills", "/shared/team-skills")`.
- Use `DefaultSkillDirs()` to follow the AgentSkills convention: `skills.FromDir(skills.DefaultSkillDirs()...)`.

---

## Recipe 2: Write a skill file

**Goal:** Author a custom skill for your domain.

```markdown
---
name: invoice-writer
description: Generate invoices in PDF format with professional formatting.
tags: [invoice, pdf, finance]
tools: [shell, file_write]
references: [oasis-pdf, oasis-design-system]
---

You are an expert at generating professional invoices.

## Required fields every invoice must include

- Invoice number (format: INV-YYYYMMDD-NNN)
- Issue date and payment due date (Net 30 default)
- Seller and buyer details (name, address, contact)
- Line items table: description, quantity, unit price, subtotal
- Subtotal, tax (if applicable), and total due
- Payment instructions

## Format rules

- Use the oasis-pdf skill to render the final output as PDF.
- Use the corporate palette from oasis-design-system.
- Place the company logo at the top-left if a path is provided.
- Bold the total due row.

## Steps

1. Ask the user for any missing required fields.
2. Write the HTML file using the structure from the oasis-pdf skill.
3. Render to PDF.
4. Confirm the output file path.
```

**Plain-English walkthrough:**

- The file lives at `./skills/invoice-writer/SKILL.md`.
- `references: [oasis-pdf, oasis-design-system]` means if you call `ActivateWithReferences`, those skills' instructions are prepended first, giving the agent PDF and design knowledge without repeating it.
- The `tools` field is advisory — it tells the LLM which tools this skill relies on, but doesn't restrict or auto-register them.
- Keep descriptions accurate and specific; the agent reads them during `skill_discover` to decide whether to activate.

**Variations:**

- Add a `{dir}` placeholder to reference a template file: `Load the base template from {dir}/templates/invoice.html.`
- Add `model: gpt-4o` to hint that this skill works best with a specific model.
- Add a `metadata:` block for versioning: `version: 1.2.0`.

---

## Recipe 3: Pre-activate a skill (always-on)

**Goal:** Skip runtime discovery and inject a skill's instructions into every LLM call.

```go
package main

import (
    "context"
    "fmt"

    "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/provider/openaicompat"
    "github.com/nevindra/oasis/skills"
)

func main() {
    provider := skills.FromDir("./skills")

    // Load the skill at startup.
    ctx := context.Background()
    skill, err := skills.ActivateWithReferences(ctx, provider, "invoice-writer")
    if err != nil {
        panic(err)
    }

    llm := openaicompat.New("https://api.openai.com/v1", "gpt-4o", "YOUR_KEY")

    // The skill's instructions are part of the system prompt on every call.
    ag := oasis.NewAgent(llm,
        oasis.WithPrompt("You are an invoice assistant."),
        oasis.WithActiveSkills(skill),
    )

    result, err := ag.Execute(ctx, oasis.AgentTask{
        Input: "Create invoice INV-20260101-001 for Acme Corp, $4,800 for consulting.",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Output)
}
```

**Plain-English walkthrough:**

- `skills.ActivateWithReferences(...)` loads the skill and prepends any referenced skills' instructions (here, `oasis-pdf` and `oasis-design-system`).
- `oasis.WithActiveSkills(skill)` injects `skill.Instructions` into the system prompt on every LLM call — no `skill_discover` or `skill_activate` call happens at runtime.
- This is faster than on-demand discovery and appropriate when the skill is always needed.

**Variations:**

- Pass multiple skills: `oasis.WithActiveSkills(skill1, skill2)`.
- Combine with a provider for other skills: use both `WithActiveSkills` and `WithSkills` on the same agent.

---

## Recipe 4: Let the agent create a skill from experience

**Goal:** Use a `SkillWriter`-backed provider so the agent can persist what it learned as a new skill.

```go
package main

import (
    "context"
    "fmt"

    "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/provider/openaicompat"
    "github.com/nevindra/oasis/skills"
)

func main() {
    // FromDir returns a provider that also implements SkillWriter.
    // skill_create and skill_update are registered automatically.
    provider := skills.FromDir("./skills")

    llm := openaicompat.New("https://api.openai.com/v1", "gpt-4o", "YOUR_KEY")

    ag := oasis.NewAgent(llm,
        oasis.WithPrompt("You are a learning assistant. "+
            "After completing a complex task, consider saving what you learned "+
            "as a skill using skill_create so future tasks are faster."),
        oasis.WithSkills(provider),
    )

    result, err := ag.Execute(context.Background(), oasis.AgentTask{
        Input: "Analyze the sales-data.csv file and show me the top 5 products by revenue. " +
            "If you develop a reusable analysis approach, save it as a skill.",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Output)
}
```

**Plain-English walkthrough:**

- `skills.FromDir("./skills")` returns a `*fileSkillProvider` which implements both `SkillProvider` and `SkillWriter`.
- Because the provider implements `SkillWriter`, `NewSkillTools` also registers `skill_create` and `skill_update`.
- The agent can call `skill_create` with a name, description, and instructions — Oasis writes the folder and `SKILL.md` file automatically.
- The new skill is immediately visible to `skill_discover` on the next call, with no restart.

**Variations:**

- To prevent the agent from writing skills to an arbitrary path, pass a single locked directory: `skills.FromDir("/secure/writable/skills")`.
- Check programmatically whether a provider supports writing: `if w, ok := provider.(skills.SkillWriter); ok { ... }`.

---

## Recipe 5: Manage skills in code (server-side API)

**Goal:** Create, update, and delete skills from your application code — no agent involved.

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/nevindra/oasis/skills"
)

func main() {
    ctx := context.Background()

    provider := skills.FromDir("./skills")
    writer, ok := provider.(skills.SkillWriter)
    if !ok {
        log.Fatal("provider does not support writing")
    }

    // Create a new skill.
    err := writer.CreateSkill(ctx, skills.Skill{
        Name:         "code-reviewer",
        Description:  "Review Go code for correctness, style, and performance.",
        Tags:         []string{"go", "code-review"},
        Instructions: "You are a senior Go engineer. Review code for:\n" +
            "1. Correctness — logic errors, off-by-ones, nil dereferences.\n" +
            "2. Style — idiomatic Go, naming, doc comments.\n" +
            "3. Performance — allocations in hot paths, unbounded slices.",
    })
    if err != nil {
        log.Fatalf("CreateSkill: %v", err)
    }
    fmt.Println("created code-reviewer")

    // Update the instructions later.
    newInstructions := "You are a senior Go engineer (updated).\n..."
    err = writer.UpdateSkill(ctx, "code-reviewer", skills.Skill{
        Name:         "code-reviewer",
        Description:  "Review Go code for correctness, style, and performance.",
        Instructions: newInstructions,
        Tags:         []string{"go", "code-review"},
    })
    if err != nil {
        log.Fatalf("UpdateSkill: %v", err)
    }
    fmt.Println("updated code-reviewer")

    // List all skills.
    summaries, err := provider.Discover(ctx)
    if err != nil {
        log.Fatalf("Discover: %v", err)
    }
    for _, s := range summaries {
        fmt.Printf("- %s: %s\n", s.Name, s.Description)
    }

    // Delete when done.
    if err := writer.DeleteSkill(ctx, "code-reviewer"); err != nil {
        log.Fatalf("DeleteSkill: %v", err)
    }
    fmt.Println("deleted code-reviewer")
}
```

**Plain-English walkthrough:**

- `skills.FromDir("./skills")` returns a concrete `*fileSkillProvider`. The type assertion to `SkillWriter` is how you check write support — built-in and chained providers will fail this check.
- `CreateSkill` writes `./skills/code-reviewer/SKILL.md` with the serialized frontmatter and instructions body.
- `UpdateSkill` finds the existing file and overwrites it completely — pass all fields you want to keep, not just the changed ones.
- `Discover` rescans the filesystem on every call — the new skill is visible immediately.

**Variations:**

- To update only specific fields programmatically: read with `Activate`, modify the returned `Skill` struct, then pass it to `UpdateSkill`.
- To build a REST API over skills: wrap `CreateSkill`, `UpdateSkill`, `DeleteSkill` in HTTP handlers — the provider is concurrency-safe.

---

## Recipe 6: Chain providers with override priority

**Goal:** Layer project skills, user skills, and built-ins so the nearest scope wins.

```go
package main

import (
    "context"
    "fmt"

    "github.com/nevindra/oasis/skills"
)

func main() {
    provider := skills.Chain(
        skills.FromDir("./.agents/skills"),          // project-level (highest priority)
        skills.FromDir(skills.DefaultSkillDirs()...), // user-level (~/.agents/skills)
    )

    summaries, _ := provider.Discover(context.Background())
    for _, s := range summaries {
        fmt.Printf("%s — %s\n", s.Name, s.Description)
    }
}
```

**Plain-English walkthrough:**

- `skills.Chain` searches providers left to right; the first provider that has a skill with a given name wins.
- `./.agents/skills` is scanned first, so a project-level `oasis-pdf` skill overrides the built-in one — useful for project-specific tweaks.
- `DefaultSkillDirs()` returns both `<cwd>/.agents/skills/` and `~/.agents/skills/` — calling `FromDir` with both again would duplicate the project path, so here we separate project-level explicitly.

**Variations:**

- Use `skills.FromDir("./.agents/skills")` and `skills.FromDir(os.ExpandEnv("$HOME/.agents/skills"))` as separate providers if you want explicit control.
- Add a database-backed `SkillProvider` implementation to pull skills from a remote store — implement `Discover` and `Activate` against your API, pass it to `Chain`.

---

## Recipe 7: Ship companion files with a skill

**Goal:** Write a skill with detailed reference material in companion files, letting the agent fetch detail on demand.

Skill file at `./skills/api-client/SKILL.md`:

```markdown
---
name: api-client
description: Call REST APIs using curl or your language's HTTP library.
tags: [http, api, rest]
---

You are an expert at making REST API calls and parsing responses.

## Quick steps

1. Ask for the API endpoint and method (GET, POST, etc.).
2. Ask for any required headers or authentication.
3. Construct the request and execute it.
4. Parse and summarize the response.

For detailed endpoint reference, see {dir}/references/endpoints.md
For error code reference, see {dir}/references/errors.md
```

Companion files at `./skills/api-client/references/`:

```
references/
  endpoints.md        ← full list of available endpoints
  errors.md           ← error codes and recovery steps
  auth-flows.md       ← authentication strategy guide
```

**How the agent uses it:**

- The agent activates `api-client` and reads the brief SKILL.md instructions.
- During task execution, if the agent needs to know the full list of endpoints, it calls `skill_read("api-client", "references/endpoints.md")`.
- Similarly for error codes or auth flows — all fetched on demand without loading everything upfront.

**Plain-English walkthrough:**

- Companion files are bundled in the skill directory alongside `SKILL.md`.
- The agent can call `skill_list_resources("api-client")` to discover available companion files (returns `["references/endpoints.md", "references/errors.md", "references/auth-flows.md"]`).
- The agent calls `skill_read` only for the files it actually needs during the task.
- Token cost is paid only for fetched files, not for all companions upfront.

---

## Recipe 8: Enable eager discovery with skill catalog and custom search

**Goal:** Inject the skill catalog into the system prompt and plug in a custom searcher for vector ranking.

```go
package main

import (
    "context"
    "fmt"

    "github.com/nevindra/oasis"
    "github.com/nevindra/oasis/provider/openaicompat"
    "github.com/nevindra/oasis/skills"
)

// MyVectorSearcher implements skills.SkillSearcher.
// It could use embeddings, semantic search, or any ranking logic.
type MyVectorSearcher struct {
    // ... your index / model / state
}

func (s *MyVectorSearcher) SearchSkills(ctx context.Context, query string, limit int) ([]skills.SkillSearchResult, error) {
    // Your implementation: embed query, find nearest neighbors, rank, return.
    // For now, a stub that returns all skills with fake scores.
    summaries, _ := s.provider.Discover(ctx) // assume you stored provider
    results := make([]skills.SkillSearchResult, 0, len(summaries))
    for _, summary := range summaries {
        results = append(results, skills.SkillSearchResult{
            SkillSummary: summary,
            Score:        0.9, // your ranking logic here
        })
    }
    return results[:min(limit, len(results))], nil
}

func main() {
    // Wrap your provider with vector search on type assertion.
    fileProvider := skills.FromDir("./skills")
    
    // If you implement SkillSearcher, the agent will use it automatically.
    // (In production, you'd compose this into the provider chain.)
    
    llm := openaicompat.New("https://api.openai.com/v1", "gpt-4o", "YOUR_KEY")
    
    ag := oasis.NewAgent(llm,
        oasis.WithSkills(fileProvider),
        oasis.WithSkillCatalog(),  // inject catalog into every request
    )
    
    result, err := ag.Execute(context.Background(), oasis.AgentTask{
        Input: "Help me set up a PDF invoice generator.",
    })
    if err != nil {
        panic(err)
    }
    fmt.Println(result.Output)
}
```

**Plain-English walkthrough:**

- `WithSkillCatalog()` adds the list of all available skills to the system prompt on every request — the LLM can browse without a tool call.
- The `skill_search` tool is always registered. If the agent wants to search by query, it calls the tool. The framework checks if your provider implements `SkillSearcher`:
  - If yes: your searcher's `SearchSkills` is called.
  - If no: the built-in BM25 searcher is used.
- You implement custom search (vector, semantic, hybrid) by wrapping or augmenting your provider and implementing `SkillSearcher`. No framework changes required.

**Variations:**

- Skip `WithSkillCatalog` if your skill count is large — inject only on demand via `skill_search`.
- Combine eager catalog with lazy search: catalog gives visibility; `skill_search` lets the agent refine via query.
- For very large corpora: implement a `SkillSearcher` backed by a persistent vector index (Qdrant, Weaviate, etc.) — the agent queries your index transparently via the `skill_search` tool.
