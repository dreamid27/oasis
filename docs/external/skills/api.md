# Skills API

Import path: `github.com/nevindra/oasis/skills`

The root `oasis` package re-exports `Skill`, `WithSkills`, `WithActiveSkills`, and `WithSkillCatalog` for convenience.

---

## Types

### `Skill`

A loaded skill. The full representation including instructions.

| Field | Type | Notes |
|---|---|---|
| `Name` | `string` | Canonical identifier. Matches the folder name on disk. |
| `Description` | `string` | Short summary used during discovery. |
| `Instructions` | `string` | Full markdown instructions injected into the agent context. After `Activate`, any `{dir}` placeholder is replaced with the absolute folder path. |
| `Tools` | `[]string` | Optional list of tool names the skill recommends. Advisory — the agent is not forced to use only these. |
| `Model` | `string` | Optional model override hint. Not enforced by the framework automatically. |
| `Tags` | `[]string` | Categorization labels. Useful for discovery filtering. |
| `References` | `[]string` | Names of other skills whose instructions should be prepended. Resolved by `ActivateWithReferences`. |
| `Dir` | `string` | Absolute path to the skill folder on disk. Empty for built-in (embedded) skills. Not serialized to JSON. |
| `Compatibility` | `string` | Free-form compatibility string (e.g., `"oasis >= 0.30"`). |
| `License` | `string` | SPDX license identifier. |
| `Metadata` | `map[string]string` | Arbitrary key-value pairs from the `metadata:` block in frontmatter. |

Zero value is valid but not useful — `Name` and `Instructions` are the meaningful fields.

---

### `SkillSummary`

A lightweight view returned by `Discover`. Only the fields needed for an agent to decide whether to activate.

| Field | Type | Notes |
|---|---|---|
| `Name` | `string` | Folder name; the value to pass to `Activate`. |
| `Description` | `string` | Short summary. |
| `Tags` | `[]string` | Categorization labels. Omitted if empty. |
| `Compatibility` | `string` | Compatibility string. |

Full instructions are not loaded during discovery — that's intentional for performance.

---

### `SkillProvider` (interface)

Abstracts the source of skills. Implementations must be safe for concurrent use.

```go
type SkillProvider interface {
    Discover(ctx context.Context) ([]SkillSummary, error)
    Activate(ctx context.Context, name string) (Skill, error)
}
```

**`Discover`** returns all available skills as summaries. Results are rescanned on every call — no caching — so newly created skills are immediately visible without restart. Returns an empty slice (not an error) when no skills exist.

**`Activate`** loads the full `Skill` for the given name. Returns a non-nil error if the skill does not exist. The `{dir}` placeholder in `Instructions` is replaced with the absolute folder path before returning.

---

### `skill_search` (tool)

Searches available skills by free-text query, returning ranked matches. Always registered by `NewSkillTools`.

**Inputs:**
- `query` (string, required) — search phrase (e.g., "PDF generation", "data analysis")
- `limit` (int, optional, default 5) — maximum number of results to return

**Output:** Array of skill summaries with relevance scores, sorted by score descending.

Uses the provider's `SkillSearcher` if it implements one (e.g., vector/hybrid search); otherwise falls back to the built-in BM25 searcher.

---

### `skill_read` (tool)

Reads a companion file bundled with a skill (e.g., `references/api.md`, `scripts/setup.sh`). Registered only when the provider implements `SkillResources`.

**Inputs:**
- `name` (string, required) — skill name
- `path` (string, required) — skill-relative path (e.g., `references/guide.md`)

**Output:** File contents as a string. Capped at 64 KB; larger files are truncated with a truncation notice.

---

### `skill_list_resources` (tool)

Lists companion files (references, scripts, assets) bundled with a skill. Registered only when the provider implements `SkillResources`.

**Inputs:**
- `name` (string, required) — skill name

**Output:** Array of relative file paths. The `SKILL.md` file itself is excluded.

---

### `SkillWriter` (interface)

Optional capability. File-based providers implement this; built-in (embedded) providers do not. Check via type assertion:

```go
if w, ok := provider.(skills.SkillWriter); ok {
    _ = w.CreateSkill(ctx, skill)
}
```

```go
type SkillWriter interface {
    CreateSkill(ctx context.Context, skill Skill) error
    UpdateSkill(ctx context.Context, name string, skill Skill) error
    DeleteSkill(ctx context.Context, name string) error
}
```

**`CreateSkill`** writes a new skill folder and `SKILL.md` in the first configured directory. Errors if `Name` is empty, `Metadata` is missing, or the skill already exists. On write failure the partially created folder is cleaned up.

**`UpdateSkill`** rewrites the `SKILL.md` of an existing skill. Searches all configured directories in order. Errors if the skill is not found.

**`DeleteSkill`** removes the skill folder and all its contents. Errors if the skill is not found.

---

## Constructors

### `FromDir(dirs ...string) SkillProvider`

Returns a `SkillProvider` (also a `SkillWriter`) that reads skills from the given directories. Searches in order — first directory wins on name collisions. Write operations target the first directory. Non-existent directories are silently skipped.

```go
provider := skills.FromDir("./skills", "./team-skills")
```

`FromDir` with no arguments is valid — it creates an empty provider that never finds anything. Useful as a placeholder in tests.

### `Chain(providers ...SkillProvider) SkillProvider`

Merges multiple providers. `Discover` returns the union, sorted by name, with the first provider winning on name collisions. `Activate` searches in order and returns the first match. Skill content is supplied by the application — the framework ships no bundled skills.

```go
provider := skills.Chain(
    skills.FromDir("./project-skills"),    // project-level (wins on collision)
    skills.FromDir(skills.DefaultSkillDirs()...), // then user-level
)
```

The returned provider does not implement `SkillWriter` even if some of its members do.

### `DefaultSkillDirs() []string`

Returns the standard AgentSkills-compatible scan paths:

- `<cwd>/.agents/skills/` — project-level
- `~/.agents/skills/` — user-level

Directories that do not exist are included in the list — `FromDir` handles missing directories gracefully.

```go
provider := skills.FromDir(skills.DefaultSkillDirs()...)
```

---

## Functions

### `ActivateWithReferences(ctx, provider, name) (Skill, error)`

Activates a skill by name and prepends instructions from any skills listed in its `References` field. References are resolved one level deep — a referenced skill's own references are not followed. Missing references are silently skipped.

```go
skill, err := skills.ActivateWithReferences(ctx, provider, "pdf-gen")
// skill.Instructions now starts with base-knowledge instructions,
// followed by a separator, followed by pdf-gen's own instructions.
```

The combined instructions format is:
```
## <referenced-skill-name>

<referenced instructions>

---

## <referenced-skill-2-name>

<referenced instructions>

---

<main skill instructions>
```

### `NewSkillTools(provider SkillProvider) []core.AnyTool`

Returns the set of skill-management tools backed by the given provider. Called automatically by the framework when you use `WithSkills` — you do not normally call this directly.

- Always returns `skill_discover`, `skill_activate`, and `skill_search`.
- Also returns `skill_create` and `skill_update` if `provider` implements `SkillWriter`.
- Also returns `skill_read` and `skill_list_resources` if `provider` implements `SkillResources`.

---

### `SkillResources` (interface)

Optional capability for reading companion files bundled with a skill. The `FromDir` provider implements it (reading from the skill folder); `Chain` forwards to whichever member provider owns the skill. Check via type assertion:

```go
if sr, ok := provider.(skills.SkillResources); ok {
    paths, _ := sr.ListResources(ctx, "skill-name")
}
```

```go
type SkillResources interface {
    ListResources(ctx context.Context, name string) ([]string, error)
    ReadResource(ctx context.Context, name, relPath string) ([]byte, error)
}
```

**`ListResources`** returns the relative paths of companion files bundled with a skill (e.g., `references/api.md`, `scripts/setup.sh`). The `SKILL.md` file itself is excluded from the list. Returns an empty slice when no companions exist.

**`ReadResource`** reads a companion file by name and relative path. The path is confined to the skill directory — absolute paths and `..` are rejected. Output is capped at 64 KB; truncation is noted in the returned bytes. Binary files are accepted; callers must handle raw bytes appropriately.

---

### `SkillSearchResult`

A scored search result returned by `SkillSearcher.SearchSkills`.

```go
type SkillSearchResult struct {
    SkillSummary         // embedded: Name, Description, Tags, Compatibility
    Score        float64 // higher is better
}
```

---

### `SkillSearcher` (interface)

Optional capability. Implement this to plug in custom search (e.g., vector/hybrid search). When a provider does not implement it, `NewSkillTools` registers the built-in BM25 searcher.

```go
type SkillSearcher interface {
    SearchSkills(ctx context.Context, query string, limit int) ([]SkillSearchResult, error)
}
```

**`SearchSkills`** returns up to `limit` skills matching the free-text query, ranked by relevance (highest score first). The `limit` parameter is a guideline; implementations may return fewer results if fewer matches exist.

---

### `NewBM25Searcher(p SkillProvider) SkillSearcher`

Returns a `SkillSearcher` using Okapi BM25 scoring over each skill's name, description, tags, and instructions. The searcher reads the provider on every query — no caching — so results are always fresh. For large skill corpora, consider implementing `SkillSearcher` yourself with a persistent index or vector embeddings.

```go
searcher := skills.NewBM25Searcher(provider)
results, err := searcher.SearchSkills(ctx, "invoice pdf", 5)
```

---

## Options (agent configuration)

### `WithSkills(provider SkillProvider) AgentOption`

Registers a skill provider. The framework calls `NewSkillTools(provider)` at agent build time and registers the resulting tools. The LLM can then discover and activate skills autonomously.

```go
ag := oasis.NewAgent(llm, oasis.WithSkills(provider))
```

### `WithActiveSkills(skills ...Skill) AgentOption`

Pre-activates one or more skills. Their instructions are appended to the system prompt on every LLM call — no tool call required. Use when you know upfront which skills are always relevant.

```go
skill, _ := provider.Activate(ctx, "data-analyst")
ag := oasis.NewAgent(llm, oasis.WithActiveSkills(skill))
```

`WithActiveSkills` and `WithSkills` can be combined: some skills are always active, others are discoverable on demand.

### `WithSkillCatalog() AgentOption`

Injects a catalog of available skill summaries (name, description, tags) into the system prompt on every request. The LLM can browse the catalog before its first tool call to choose a skill proactively, enabling eager skill discovery.

```go
ag := oasis.NewAgent(llm,
    oasis.WithSkills(provider),
    oasis.WithSkillCatalog(),
)
```

The catalog is recomputed fresh on each request and excludes skills already injected via `WithActiveSkills`. This is a no-op if no provider is configured via `WithSkills`. Use `skill_search` or `skill_discover` tools for lazy, on-demand discovery — `WithSkillCatalog` is complementary, trading token cost for eager visibility.

---

## The `SKILL.md` file format

Skills are stored as directories. The directory name is the skill's canonical identifier. The `SKILL.md` file uses YAML frontmatter (delimited by `---`) followed by the instruction body.

```markdown
---
name: data-analyst
description: Analyze datasets, produce summaries, identify trends.
tags: [data, analytics, csv]
tools: [shell, file_read]
model: gpt-4o
references: [base-statistics]
compatibility: oasis >= 0.30
license: MIT
metadata:
  author: your-name
  version: 1.0.0
---

You are an expert data analyst. When given a dataset:

1. Inspect the schema first — `head`, column names, data types.
2. Look for nulls, outliers, and format inconsistencies before drawing conclusions.
3. Summarize findings in plain language the user can act on.
```

**Frontmatter field reference:**

| Key | Required | Notes |
|---|---|---|
| `name` | Recommended | Falls back to folder name if absent. |
| `description` | Yes (for discovery) | Shown in `skill_discover` output; used by the LLM to decide whether to activate. |
| `tags` | No | Inline array: `[go, data, pdf]` |
| `tools` | No | Inline array of tool names the skill recommends. |
| `model` | No | Model override hint. |
| `references` | No | Inline array of skill names to prepend when using `ActivateWithReferences`. |
| `compatibility` | No | Free-form compatibility string. |
| `license` | No | SPDX identifier. |
| `metadata` | No | Nested key-value block. Accessible as `Skill.Metadata`. |

The `{dir}` placeholder in the instructions body is replaced at activation time with the absolute path to the skill folder. Use it to reference assets like templates or config files:

```markdown
Load the invoice template from {dir}/templates/invoice.html.
```

---

## Errors

| Situation | Behavior |
|---|---|
| `Activate` with unknown name | Returns `error: skill "X" not found` |
| `CreateSkill` with empty `Name` | Returns error immediately; no disk write |
| `CreateSkill` when skill already exists | Returns error; no overwrite |
| `UpdateSkill` / `DeleteSkill` with unknown name | Returns error |
| `Discover` on non-existent directory | Silently skipped; no error returned |
| `parseFrontmatter` on malformed file | Skill is silently skipped during `Discover`; `Activate` returns error |
| `ActivateWithReferences` with missing reference | Missing reference is skipped; skill still activates successfully |

---

## Thread safety

All exported types (`fileSkillProvider`, `chainedSkillProvider`) are safe for concurrent use. `Discover` rescans the filesystem on every call with no shared mutable state. `CreateSkill` / `UpdateSkill` / `DeleteSkill` do not hold locks across file operations — concurrent writes to the same skill name are not protected; callers should serialize writes if needed.
