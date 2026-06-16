package subscribers

import "github.com/zarldev/zarlmono/zarlai/service"

// Template keys used by end-of-session subscribers. Exposed so
// cmd/zarl's startup code can call RegisterTemplates with a store the
// admin UI will then expose for editing.
const (
	TemplateSummarizerSystem = "subscribers.summarizer.system"
	TemplateExtractorSystem  = "subscribers.extractor.system"
)

// RegisterTemplates seeds the code defaults for every subscriber's
// system prompt in a PromptTemplateStore. Call once at startup after
// the store is constructed, before the first session-ended event.
//
// Placeholders use {{key}} substitution; the subscriber fills them in
// at render time with per-event values.
func RegisterTemplates(store *service.MemoryPromptTemplateStore) {
	store.RegisterDefault(TemplateSummarizerSystem,
		"Summarize this conversation for {{person_name}} in 2-3 sentences. "+
			"What was discussed, what was decided, any open threads. "+
			"Write in second person ('you asked about…', 'you decided…') since the summary will be replayed to {{person_name}} in future sessions. "+
			"Be concise and factual.",
	)
	store.RegisterDefault(TemplateExtractorSystem,
		"Extract DURABLE facts about {{person_name}} from this conversation — things that will still be true and useful a month from now. "+
			"Capture: stable preferences, identifiers (names, addresses, contact info), relationships, possessions/equipment, recurring routines, scheduled commitments with dates, biographical facts. "+
			"DO NOT capture: anything {{person_name}} just asked / requested / wanted / queued / played / paused / searched / looked up in this session — those are turn-scoped actions, not facts. "+
			"DO NOT capture: in-the-moment behaviour (made noises, drank a drink, decided to hold off, considers X classic), session-scoped intent ('wants to search …', 'is researching …'), or vague generalities ('uses Home Assistant', 'maintains a smart climate system' — already obvious from the tool list). "+
			"Test each candidate: would a stranger reading it next month learn something useful, or would it read as a stale log entry? If the latter, drop it. "+
			"Return as a bulleted list, one fact per line starting with '- '. Each bullet starts with '{{person_name}} ' and uses present tense (is / has / prefers / likes / drives / lives at). One atomic fact per bullet, no compound sentences, no meta-narration about the conversation itself. "+
			"If nothing qualifies, return an empty response — do NOT emit a bullet explaining why.{{already_known_block}}",
	)
}
