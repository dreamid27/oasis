package skills

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nevindra/oasis/core"
)

// maxResourceBytes caps skill_read output to protect the model context window.
const maxResourceBytes = 64 * 1024

// NewSkillTools returns the set of skill-management tools backed by the given
// SkillProvider. skill_discover, skill_activate, and skill_search are always
// returned. skill_create and skill_update are included only when the provider
// implements SkillWriter; skill_read and skill_list_resources only when it
// implements SkillResources. skill_search uses the provider's own SkillSearcher
// when present, else a built-in BM25 searcher.
func NewSkillTools(provider SkillProvider) []core.AnyTool {
	tools := []core.AnyTool{
		core.Erase[skillDiscoverIn, string](&skillDiscoverTool{provider: provider}),
		core.Erase[skillActivateIn, string](&skillActivateTool{provider: provider}),
	}

	// Search is always available: prefer the provider's own SkillSearcher,
	// else fall back to the built-in BM25 searcher.
	searcher, ok := provider.(SkillSearcher)
	if !ok {
		searcher = NewBM25Searcher(provider)
	}
	tools = append(tools, core.Erase[skillSearchIn, string](&skillSearchTool{searcher: searcher}))

	if w, ok := provider.(SkillWriter); ok {
		tools = append(tools,
			core.Erase[skillCreateIn, string](&skillCreateTool{writer: w}),
			core.Erase[skillUpdateIn, string](&skillUpdateTool{provider: provider, writer: w}),
		)
	}
	if r, ok := provider.(SkillResources); ok {
		tools = append(tools,
			core.Erase[skillListResourcesIn, string](&skillListResourcesTool{resources: r}),
			core.Erase[skillReadIn, string](&skillReadTool{resources: r}),
		)
	}
	return tools
}

// --- skill_discover ---

// skillDiscoverIn has no fields; an empty In schema reflects to
// {"type":"object","properties":{}}.
type skillDiscoverIn struct{}

type skillDiscoverTool struct {
	provider SkillProvider
}

func (t *skillDiscoverTool) Definition() core.ToolMeta {
	return core.ToolMeta{
		Name:        "skill_discover",
		Description: "List all available skills with their names, descriptions, and tags. Use this to browse what skills exist before activating one.",
	}
}

func (t *skillDiscoverTool) Execute(ctx context.Context, _ skillDiscoverIn) (string, error) {
	summaries, err := t.provider.Discover(ctx)
	if err != nil {
		return "", fmt.Errorf("discover failed: %w", err)
	}
	if len(summaries) == 0 {
		return "no skills available", nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "%d skill(s) available:\n\n", len(summaries))
	for i, s := range summaries {
		fmt.Fprintf(&out, "%d. %s\n   %s\n", i+1, s.Name, s.Description)
		if len(s.Tags) > 0 {
			fmt.Fprintf(&out, "   Tags: %s\n", strings.Join(s.Tags, ", "))
		}
		fmt.Fprintln(&out)
	}
	return out.String(), nil
}

// --- skill_activate ---

type skillActivateIn struct {
	Name string `json:"name" describe:"The name of the skill to activate"`
}

type skillActivateTool struct {
	provider SkillProvider
}

func (t *skillActivateTool) Definition() core.ToolMeta {
	return core.ToolMeta{
		Name:        "skill_activate",
		Description: "Load the full instructions for a skill by name. Returns complete metadata and instructions that can be applied to the current task.",
	}
}

func (t *skillActivateTool) Execute(ctx context.Context, in skillActivateIn) (string, error) {
	if in.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	sk, err := t.provider.Activate(ctx, in.Name)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Skill: %s\n", sk.Name)
	fmt.Fprintf(&out, "Description: %s\n", sk.Description)
	if len(sk.Tags) > 0 {
		fmt.Fprintf(&out, "Tags: %s\n", strings.Join(sk.Tags, ", "))
	}
	if len(sk.Tools) > 0 {
		fmt.Fprintf(&out, "Tools: %s\n", strings.Join(sk.Tools, ", "))
	}
	if sk.Model != "" {
		fmt.Fprintf(&out, "Model: %s\n", sk.Model)
	}
	if len(sk.References) > 0 {
		fmt.Fprintf(&out, "References: %s\n", strings.Join(sk.References, ", "))
	}
	fmt.Fprintf(&out, "\nInstructions:\n%s\n", sk.Instructions)
	return out.String(), nil
}

// --- skill_create ---

type skillCreateIn struct {
	Name         string   `json:"name" describe:"Short identifier for the skill (e.g. code-reviewer, data-analyst)"`
	Description  string   `json:"description" describe:"What this skill does, used for discovery matching"`
	Instructions string   `json:"instructions" describe:"Detailed instructions injected into the agent system prompt when this skill is active"`
	Tags         []string `json:"tags,omitempty" describe:"Optional categorization labels"`
	Tools        []string `json:"tools,omitempty" describe:"Optional list of tool names this skill should use (empty = all)"`
	Model        string   `json:"model,omitempty" describe:"Optional model override"`
	References   []string `json:"references,omitempty" describe:"Optional skill names this skill builds on"`
}

type skillCreateTool struct {
	writer SkillWriter
}

func (t *skillCreateTool) Definition() core.ToolMeta {
	return core.ToolMeta{
		Name:        "skill_create",
		Description: "Create a new skill from experience. A skill is a stored instruction package that can specialize agent behavior for specific tasks.",
	}
}

func (t *skillCreateTool) Execute(ctx context.Context, in skillCreateIn) (string, error) {
	if in.Name == "" || in.Description == "" || in.Instructions == "" {
		return "", fmt.Errorf("name, description, and instructions are required")
	}
	sk := Skill{
		Name:         in.Name,
		Description:  in.Description,
		Instructions: in.Instructions,
		Tags:         in.Tags,
		Tools:        in.Tools,
		Model:        in.Model,
		References:   in.References,
	}
	if err := t.writer.CreateSkill(ctx, sk); err != nil {
		return "", err
	}
	return fmt.Sprintf("created skill %q", sk.Name), nil
}

// --- skill_update ---

type skillUpdateIn struct {
	Name         string   `json:"name" describe:"Name of the skill to update"`
	Description  *string  `json:"description,omitempty" describe:"New description"`
	Instructions *string  `json:"instructions,omitempty" describe:"New instructions"`
	Tags         []string `json:"tags,omitempty" describe:"New tags (replaces existing)"`
	Tools        []string `json:"tools,omitempty" describe:"New tool list (replaces existing)"`
	Model        *string  `json:"model,omitempty" describe:"New model override"`
	References   []string `json:"references,omitempty" describe:"New skill references (replaces existing)"`
}

type skillUpdateTool struct {
	provider SkillProvider
	writer   SkillWriter
}

func (t *skillUpdateTool) Definition() core.ToolMeta {
	return core.ToolMeta{
		Name:        "skill_update",
		Description: "Update an existing skill. Only provided fields are changed; omitted fields keep their current values.",
	}
}

func (t *skillUpdateTool) Execute(ctx context.Context, in skillUpdateIn) (string, error) {
	if in.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	existing, err := t.provider.Activate(ctx, in.Name)
	if err != nil {
		return "", fmt.Errorf("cannot update: %w", err)
	}

	var changes []string
	if in.Description != nil {
		existing.Description = *in.Description
		changes = append(changes, "description")
	}
	if in.Instructions != nil {
		existing.Instructions = *in.Instructions
		changes = append(changes, "instructions")
	}
	if in.Tags != nil {
		existing.Tags = in.Tags
		changes = append(changes, "tags")
	}
	if in.Tools != nil {
		existing.Tools = in.Tools
		changes = append(changes, "tools")
	}
	if in.Model != nil {
		existing.Model = *in.Model
		changes = append(changes, "model")
	}
	if in.References != nil {
		existing.References = in.References
		changes = append(changes, "references")
	}
	if len(changes) == 0 {
		return "no changes specified", nil
	}
	if err := t.writer.UpdateSkill(ctx, in.Name, existing); err != nil {
		return "", err
	}
	return fmt.Sprintf("updated skill %q: %s", existing.Name, strings.Join(changes, ", ")), nil
}

// --- skill_search ---

type skillSearchIn struct {
	Query string `json:"query" describe:"Free-text description of the task or topic to find a skill for"`
	Limit int    `json:"limit,omitempty" describe:"Maximum number of results (default 5)"`
}

type skillSearchTool struct {
	searcher SkillSearcher
}

func (t *skillSearchTool) Definition() core.ToolMeta {
	return core.ToolMeta{
		Name:        "skill_search",
		Description: "Search available skills by topic or task description; returns the best matches ranked by relevance. Use this to find a skill when you don't know its exact name.",
	}
}

func (t *skillSearchTool) Execute(ctx context.Context, in skillSearchIn) (string, error) {
	if in.Query == "" {
		return "", fmt.Errorf("query is required")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	results, err := t.searcher.SearchSkills(ctx, in.Query, limit)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	if len(results) == 0 {
		return "no matching skills found", nil
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%d matching skill(s):\n\n", len(results))
	for i, r := range results {
		fmt.Fprintf(&out, "%d. %s (score %.2f)\n   %s\n", i+1, r.Name, r.Score, r.Description)
		if len(r.Tags) > 0 {
			fmt.Fprintf(&out, "   Tags: %s\n", strings.Join(r.Tags, ", "))
		}
		fmt.Fprintln(&out)
	}
	return out.String(), nil
}

// --- skill_list_resources ---

type skillListResourcesIn struct {
	Name string `json:"name" describe:"The name of the skill whose companion files to list"`
}

type skillListResourcesTool struct {
	resources SkillResources
}

func (t *skillListResourcesTool) Definition() core.ToolMeta {
	return core.ToolMeta{
		Name:        "skill_list_resources",
		Description: "List the companion files (references, scripts, assets) bundled with a skill. Use skill_read to read one by its path.",
	}
}

func (t *skillListResourcesTool) Execute(ctx context.Context, in skillListResourcesIn) (string, error) {
	if in.Name == "" {
		return "", fmt.Errorf("name is required")
	}
	files, err := t.resources.ListResources(ctx, in.Name)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return fmt.Sprintf("skill %q has no companion files", in.Name), nil
	}
	var out strings.Builder
	fmt.Fprintf(&out, "%d file(s) in skill %q:\n", len(files), in.Name)
	for _, f := range files {
		fmt.Fprintf(&out, "- %s\n", f)
	}
	return out.String(), nil
}

// --- skill_read ---

type skillReadIn struct {
	Name string `json:"name" describe:"The name of the skill that owns the file"`
	Path string `json:"path" describe:"Skill-relative path of the companion file (e.g. references/api.md)"`
}

type skillReadTool struct {
	resources SkillResources
}

func (t *skillReadTool) Definition() core.ToolMeta {
	return core.ToolMeta{
		Name:        "skill_read",
		Description: "Read a companion file bundled with a skill (references, scripts, assets) by its skill-relative path. Call skill_list_resources first to see what files exist.",
	}
}

func (t *skillReadTool) Execute(ctx context.Context, in skillReadIn) (string, error) {
	if in.Name == "" || in.Path == "" {
		return "", fmt.Errorf("name and path are required")
	}
	data, err := t.resources.ReadResource(ctx, in.Name, in.Path)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return fmt.Sprintf("binary file %q (%d bytes); not shown", in.Path, len(data)), nil
	}
	if len(data) > maxResourceBytes {
		// Trim back to a rune boundary so truncation never splits a UTF-8 rune.
		cut := maxResourceBytes
		for cut > 0 && !utf8.RuneStart(data[cut]) {
			cut--
		}
		return string(data[:cut]) + "\n\n[truncated: file exceeds 64KB]", nil
	}
	return string(data), nil
}

// Compile-time interface checks.
var (
	_ core.Tool[skillDiscoverIn, string]      = (*skillDiscoverTool)(nil)
	_ core.Tool[skillActivateIn, string]      = (*skillActivateTool)(nil)
	_ core.Tool[skillCreateIn, string]        = (*skillCreateTool)(nil)
	_ core.Tool[skillUpdateIn, string]        = (*skillUpdateTool)(nil)
	_ core.Tool[skillSearchIn, string]        = (*skillSearchTool)(nil)
	_ core.Tool[skillListResourcesIn, string] = (*skillListResourcesTool)(nil)
	_ core.Tool[skillReadIn, string]          = (*skillReadTool)(nil)
)
