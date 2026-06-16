package task

// Primary languages in the SWE-bench Multilingual dataset. Named
// constants so the repoLanguage table reads as a typed mapping rather
// than a wall of repeated string literals.
const (
	langC          = "c"
	langCPP        = "cpp"
	langGo         = "go"
	langJava       = "java"
	langJavaScript = "javascript"
	langPHP        = "php"
	langRuby       = "ruby"
	langRust       = "rust"
	langTypeScript = "typescript"
)

// repoLanguage maps every SWE-bench Multilingual repo to its primary
// language. The dataset (300 tasks across 42 repos) doesn't ship a
// per-row language column; we infer from the repo name using this
// static table, derived from the SWE-bench Multilingual leaderboard.
//
// When a new shard is published with additional repos, add them here
// rather than relying on heuristic-driven inference — explicit beats
// clever for a benchmark dataset.
var repoLanguage = map[string]string{
	// Go
	"gin-gonic/gin":         langGo,
	"prometheus/prometheus": langGo,
	"caddyserver/caddy":     langGo,
	"gohugoio/hugo":         langGo,
	"hashicorp/terraform":   langGo,

	// Java
	"google/gson":           langJava,
	"javaparser/javaparser": langJava,
	"projectlombok/lombok":  langJava,
	"reactivex/rxjava":      langJava,
	"apache/druid":          langJava,
	"apache/lucene":         langJava,

	// JavaScript / TypeScript
	"mrdoob/three.js":           langJavaScript,
	"axios/axios":               langJavaScript,
	"vuejs/core":                langTypeScript,
	"preactjs/preact":           langTypeScript,
	"babel/babel":               langTypeScript,
	"immutable-js/immutable-js": langTypeScript,
	"facebook/docusaurus":       langTypeScript,

	// PHP
	"briannesbitt/carbon":       langPHP,
	"laravel/framework":         langPHP,
	"phpoffice/phpspreadsheet":  langPHP,
	"php-cs-fixer/php-cs-fixer": langPHP,

	// Ruby
	"faker-ruby/faker":  langRuby,
	"rubocop/rubocop":   langRuby,
	"jekyll/jekyll":     langRuby,
	"fastlane/fastlane": langRuby,
	"jordansissel/fpm":  langRuby,
	"fluent/fluentd":    langRuby,

	// Rust
	"tokio-rs/tokio":     langRust,
	"tokio-rs/axum":      langRust,
	"sharkdp/bat":        langRust,
	"burntsushi/ripgrep": langRust,
	"nushell/nushell":    langRust,
	"uutils/coreutils":   langRust,
	"astral-sh/ruff":     langRust,

	// C
	"jqlang/jq":               langC,
	"redis/redis":             langC,
	"micropython/micropython": langC,
	"valkey-io/valkey":        langC,

	// C++
	"nlohmann/json": langCPP,
	"fmtlib/fmt":    langCPP,
}

// LanguageFor returns the primary language for a SWE-bench repo, or
// empty when the repo isn't in the Multilingual dataset (defensive —
// the loader populates Spec.Language via this helper, so unknown
// repos surface as "no language" rather than crashing).
func LanguageFor(repo string) string {
	return repoLanguage[repo]
}
