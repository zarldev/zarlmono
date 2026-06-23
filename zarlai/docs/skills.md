# Skills

Skills are short markdown capability guides — "how to research a topic
well", "how to organize a daily note", "how to plan a smart-home
routine" — that the model loads into its system prompt on the turns
where they're relevant. They live in Dolt (`skills` table, migration
`016_skills.sql`), are served by `service.SkillSelector`, and are
managed at runtime via `/admin → Skills`.

A skill is **not** the same as the active prompt. The prompt sets
zarl's persona and the universal rules; skills add task-specific
playbooks on demand. Most users will never need to touch the prompt —
they add skills.

The implementation is intentionally a hard-prompt control plane, not model training: a skill is ordinary text selected for the current turn. That makes behaviour inspectable and provider-portable, but it also means skill text should be treated like source code — small, reviewed, attributable, and measured by whether it helps the turns where it fires.

## When a skill fires

`SkillSelector` mirrors `ToolSelector`:

1. Each skill's *description* is embedded once and cached.
2. On every turn, the user's latest message is embedded.
3. The top-K closest descriptions (default K=3, see
   `WithSkillSelectorTopK`) plus any always-on skills are unioned.
4. Their markdown bodies are concatenated and shipped as part of the
   system prompt for that turn only.
5. `SkillSource.Version()` ticks on any admin edit / proposal approval,
   triggering the selector to rebuild its embedding index.

## Writing a skill

Three fields you control:

| Field | What it's for |
|---|---|
| `name` | Stable human identifier (`weekly_review_process`). Used for "always-on" lists and logs. Don't rename casually. |
| `description` | One sentence that **starts the way the user would phrase the request** ("When the user asks to plan a meal …"). The selector matches against this — it's effectively the skill's trigger. |
| `markdown` | The body that gets pasted into the system prompt when the skill fires. |

A typical body:

```markdown
## Weekly Review

**Goal:** Surface what the user worked on, what's open, what needs
follow-up — without making them dig.

### Procedure

1. Pull the last seven days of conversation summaries.
2. Group by topic. Bubble up unfinished threads.
3. Offer a short list of next actions, not a long recap.

### Tone

Concise. Bullets, not paragraphs. The user is reviewing their week,
not reading a report.
```

### Profile binding

Each skill has a `profile_binding`:

- empty / NULL → **global** (fires under any task profile, including
  the live conversation)
- `"default"` / `"researcher"` / `"coder"` → fires only when the
  active profile matches (see `taskrunner/profile.go`)

Bind a skill to a profile when its advice is profile-specific. A
"coder" skill that talks about reading source files shouldn't show up
in casual conversation; a "research note organization" skill that
references your vault probably should be global.

### Always-on skills

`WithAlwaysOnSkills(...)` names skills that ship every turn,
bypassing the semantic ranking. Use sparingly — each one adds its
full markdown body to the prompt forever, eating tokens. Reserve for
universal guidance (e.g. "how to use memory") that genuinely applies
to every interaction.

## Prompt discipline

Prefer observable, task-specific guidance over broad personality prose. A good skill says when it applies, what procedure to follow, and what output shape helps the user. Keep bodies concise enough that selecting the skill is a clear win over spending the same context on conversation history or tool output.

Because skills are selected prompt fragments, attribution matters: use stable names, accurate descriptions, and bodies that are narrow enough to inspect when a turn goes wrong. If the guidance really belongs on every turn, put it in the active prompt instead of hiding it inside an always-on skill; if the behaviour is better expressed as a tool affordance, build the tool rather than adding more markdown.

## Pitfalls

- **Don't hardcode tool names.** Skills end up in the system prompt,
  so the no-tool-names rule applies the same as it does to prompts:
  speak in capabilities ("the knowledge-base tools", "the memory
  tools"), not literals (`obsidian_simple_search`,
  `remember_about_person`). Tools carry their own descriptions; if a
  tool needs richer guidance, improve *its* description. A skill that
  names tools rots the moment the tool is renamed or swapped.

- **Description sentences, not labels.** "Weekly review" matches
  poorly. "When the user asks for a status update or wants to review
  what they've been working on" matches well — the selector embeds
  user messages, not titles.
- **Keep bodies short.** Skill markdown joins the system prompt for
  the turn. 200–600 tokens is a healthy range. Reach for tools, not
  longer skills, when guidance gets procedural.
- **Don't reinvent the active prompt.** If something belongs in every
  turn forever (persona, universal safety rules), it goes in the
  active prompt, not an always-on skill.

## Self-improvement: proposals

`skill_proposals` mirrors `prompt_proposals`. The LLM (typically the
taskrunner) writes a proposal — new skill or update to an existing
one — with a rationale. An operator approves or rejects via
`/admin → Proposals`; approval flips the live row and the selector
re-indexes on its next turn.

Sources: `repository/skill.go`, `repository/skill_proposal.go`,
`service/skill.go`, `service/skill_selector.go`. RPCs:
`AdminService.{ListSkills, CreateSkill, UpdateSkill, DeleteSkill,
ListSkillProposals, ReviewSkillProposal}`.
