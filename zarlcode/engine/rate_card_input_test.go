package engine_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zarlcode/engine"
	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

const rateCardInput = `{
  "version": 1,
  "target": {"provider":" gateway/配置 ","model":" Team/模型 Alias ","base_url":"https://Example.test:443/gateway/%76One/","protocol":"chat_completions"},
  "card": {
    "id":"local policy", "revision":"operator-r7", "entry":"Team/模型 Alias ",
    "pricing_provider":"organization/配置", "currency":"credits/EUR", "billing":"token-metered",
    "applicable":true, "token_only":false,
    "fresh_input_per_million":1.25, "cache_read_per_million":null, "output_per_million":0,
    "cache_writes":[{"lifetime_seconds":300,"per_million":2.5},{"lifetime_seconds":3600,"per_million":0},{"lifetime_seconds":1,"per_million":null},{"lifetime_seconds":2}]
  }
}`

func configuredRateCardTarget() engine.RateCardTarget {
	return engine.RateCardTarget{
		Provider: " gateway/配置 ", Model: llm.ModelID(" Team/模型 Alias "),
		BaseURL: "https://Example.test:443/gateway/%76One/", Protocol: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS,
	}
}

// A nil replacement deletes a field; all other replacements are literal JSON.
func rateCardWire(t *testing.T, scope, field string, replacement []byte) []byte {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rateCardInput), &root); err != nil {
		t.Fatal(err)
	}
	object := root
	if scope != "" {
		object = make(map[string]json.RawMessage)
		if err := json.Unmarshal(root[scope], &object); err != nil {
			t.Fatal(err)
		}
	}
	if replacement == nil {
		delete(object, field)
	} else {
		object[field] = replacement
	}
	if scope != "" {
		encoded, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		root[scope] = encoded
	}
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseConfiguredCard(t *testing.T, data []byte, target engine.RateCardTarget) llm.RateCard {
	t.Helper()
	config, err := engine.ParseRateCardConfig(data, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	card, err := config.Select(target)
	if err != nil {
		t.Fatal(err)
	}
	return card
}

func TestRateCardConfigPolicyAndProvenance(t *testing.T) {
	observedAt := time.Date(2026, time.November, 12, 4, 3, 2, 1, time.UTC)
	data := []byte(rateCardInput)
	config, err := engine.ParseRateCardConfig(data, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	card, err := config.Select(configuredRateCardTarget())
	if err != nil {
		t.Fatal(err)
	}
	if card.Reference != (llm.RateCardReference{ID: "local policy", Revision: "operator-r7", Entry: "Team/模型 Alias "}) {
		t.Fatalf("reference = %#v", card.Reference)
	}
	if card.PricingProvider != "organization/配置" || card.Currency != "credits/EUR" ||
		card.Billing != llm.BillingModes.BILLINGTOKENMETERED || !card.Applicable || card.TokenOnly {
		t.Fatalf("policy = %#v", card)
	}
	if card.FreshInput != (llm.TokenRate{PerMillionTokens: 1.25, Known: true}) || card.CacheRead.Known ||
		card.Output != (llm.TokenRate{Known: true}) {
		t.Fatalf("scalar rates = %#v, %#v, %#v", card.FreshInput, card.CacheRead, card.Output)
	}
	if len(card.CacheWrites) != 4 || card.CacheWrites[0].Lifetime != 5*time.Minute ||
		card.CacheWrites[0].Rate != (llm.TokenRate{PerMillionTokens: 2.5, Known: true}) ||
		card.CacheWrites[1].Lifetime != time.Hour || card.CacheWrites[1].Rate != (llm.TokenRate{Known: true}) ||
		card.CacheWrites[2].Rate.Known || card.CacheWrites[3].Rate.Known {
		t.Fatalf("cache writes = %#v", card.CacheWrites)
	}
	digest := sha256.Sum256(data)
	want := llm.MetadataProvenance{
		Source: llm.MetadataSources.METADATACONFIGURATION, Revision: hex.EncodeToString(digest[:]), ObservedAt: observedAt,
	}
	if card.Provenance != want {
		t.Fatalf("provenance = %#v, want %#v", card.Provenance, want)
	}
	if card.Provenance.Revision == card.Reference.Revision {
		t.Fatal("source revision conflated with user card revision")
	}
	// Whitespace and rate changes both identify different content, without
	// rewriting the operator's reference revision or old selected snapshots.
	for _, revised := range []string{rateCardInput + "\n", strings.Replace(rateCardInput, "1.25", "1.5", 1)} {
		next := parseConfiguredCard(t, []byte(revised), configuredRateCardTarget())
		if next.Provenance.Revision == card.Provenance.Revision || next.Reference != card.Reference {
			t.Fatalf("revision separation = %#v", next)
		}
	}
	if card.FreshInput.PerMillionTokens != 1.25 {
		t.Fatal("old snapshot repriced")
	}
}

func TestRateCardConfigExactSelection(t *testing.T) {
	config, err := engine.ParseRateCardConfig([]byte(rateCardInput), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		change func(*engine.RateCardTarget)
	}{
		{"provider", func(v *engine.RateCardTarget) { v.Provider = "other" }},
		{"provider spaces", func(v *engine.RateCardTarget) { v.Provider = strings.TrimSpace(v.Provider) }},
		{"alias spaces", func(v *engine.RateCardTarget) { v.Model = llm.ModelID(strings.TrimSpace(string(v.Model))) }},
		{"alias case", func(v *engine.RateCardTarget) { v.Model = " Team/模型 alias " }},
		{"canonical model", func(v *engine.RateCardTarget) { v.Model = "upstream/model" }},
		{"URL case", func(v *engine.RateCardTarget) { v.BaseURL = strings.ToLower(v.BaseURL) }},
		{"URL slash", func(v *engine.RateCardTarget) { v.BaseURL = strings.TrimSuffix(v.BaseURL, "/") }},
		{"URL escapes", func(v *engine.RateCardTarget) { v.BaseURL = strings.Replace(v.BaseURL, "%76", "v", 1) }},
		{"URL default", func(v *engine.RateCardTarget) { v.BaseURL = "" }},
		{"protocol", func(v *engine.RateCardTarget) { v.Protocol = llm.ModelProtocols.PROTOCOLRESPONSES }},
		{"unknown protocol", func(v *engine.RateCardTarget) { v.Protocol = llm.ModelProtocols.PROTOCOLUNKNOWN }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			target := configuredRateCardTarget()
			tc.change(&target)
			card, err := config.Select(target)
			if !errors.Is(err, engine.ErrRateCardTargetMismatch) || card.Reference.ID != "" {
				t.Fatalf("selection = %#v, %v", card, err)
			}
		})
	}
	var empty engine.RateCardConfig
	if _, err := empty.Select(engine.RateCardTarget{}); !errors.Is(err, engine.ErrRateCardTargetMismatch) {
		t.Fatalf("empty selection = %v", err)
	}
	defaultConfig, err := engine.ParseRateCardConfig(rateCardWire(t, "target", "base_url", []byte(`""`)), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	target := configuredRateCardTarget()
	if _, err := defaultConfig.Select(target); !errors.Is(err, engine.ErrRateCardTargetMismatch) {
		t.Fatalf("empty URL matched override: %v", err)
	}
	target.BaseURL = ""
	if _, err := defaultConfig.Select(target); err != nil {
		t.Fatalf("registered default: %v", err)
	}
}

func TestRateCardConfigCloneIsolation(t *testing.T) {
	data := []byte(rateCardInput)
	config, err := engine.ParseRateCardConfig(data, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	clone := config.Clone()
	clear(data)
	first, err := config.Select(configuredRateCardTarget())
	if err != nil {
		t.Fatal(err)
	}
	first.CacheWrites[0].Rate.PerMillionTokens = 999
	first.Reference.Revision = "mutated"
	first.Currency = "changed"
	for _, snapshot := range []engine.RateCardConfig{config, clone} {
		card, err := snapshot.Select(configuredRateCardTarget())
		if err != nil {
			t.Fatal(err)
		}
		if card.CacheWrites[0].Rate.PerMillionTokens != 2.5 || card.Reference.Revision != "operator-r7" || card.Currency != "credits/EUR" {
			t.Fatalf("selection retained mutable alias: %#v", card)
		}
		card.CacheWrites[0].Lifetime = 7 * time.Hour
	}
	card, err := config.Select(configuredRateCardTarget())
	if err != nil || card.CacheWrites[0].Lifetime != 5*time.Minute {
		t.Fatalf("repeated selection aliases = %#v, %v", card, err)
	}
}

func TestRateCardConfigRatePresence(t *testing.T) {
	values := []struct {
		name string
		wire []byte
		want llm.TokenRate
	}{
		{"absent", nil, llm.TokenRate{}},
		{"null", []byte(`null`), llm.TokenRate{}},
		{"zero", []byte(`0`), llm.TokenRate{Known: true}},
		{"negative zero", []byte(`-0`), llm.TokenRate{Known: true}},
		{"decimal zero", []byte(`0.000e-999999999`), llm.TokenRate{Known: true}},
		{"finite", []byte(`4.25`), llm.TokenRate{PerMillionTokens: 4.25, Known: true}},
		{"smallest nonzero", []byte(`5e-324`), llm.TokenRate{PerMillionTokens: math.SmallestNonzeroFloat64, Known: true}},
	}
	for _, field := range []string{"fresh_input_per_million", "cache_read_per_million", "output_per_million"} {
		for _, tc := range values {
			t.Run(field+"/"+tc.name, func(t *testing.T) {
				card := parseConfiguredCard(t, rateCardWire(t, "card", field, tc.wire), configuredRateCardTarget())
				got := map[string]llm.TokenRate{
					"fresh_input_per_million": card.FreshInput, "cache_read_per_million": card.CacheRead, "output_per_million": card.Output,
				}[field]
				if got != tc.want {
					t.Fatalf("rate = %#v, want %#v", got, tc.want)
				}
				if len(card.CacheWrites) != 4 || !card.CacheWrites[0].Rate.Known {
					t.Fatal("independently known cache rate lost")
				}
			})
		}
	}
	for _, wire := range [][]byte{nil, []byte(`[]`)} {
		card := parseConfiguredCard(t, rateCardWire(t, "card", "cache_writes", wire), configuredRateCardTarget())
		if len(card.CacheWrites) != 0 || !card.FreshInput.Known || !card.Output.Known {
			t.Fatalf("independent rate presence = %#v", card)
		}
	}
	card := parseConfiguredCard(t, rateCardWire(t, "card", "cache_writes", []byte(`[{"lifetime_seconds":9223372036,"per_million":0}]`)), configuredRateCardTarget())
	if card.CacheWrites[0].Lifetime != time.Duration(9223372036)*time.Second || !card.CacheWrites[0].Rate.Known {
		t.Fatalf("maximum whole seconds = %#v", card.CacheWrites)
	}
	for _, field := range []string{"applicable", "token_only"} {
		card := parseConfiguredCard(t, rateCardWire(t, "card", field, []byte(`false`)), configuredRateCardTarget())
		if (field == "applicable" && card.Applicable) || (field == "token_only" && card.TokenOnly) {
			t.Fatal("false assertion repaired")
		}
	}
}

func TestRateCardConfigProtocolBoundary(t *testing.T) {
	for protocol := range llm.ModelProtocols.All() {
		if protocol == llm.ModelProtocols.PROTOCOLUNKNOWN {
			continue
		}
		t.Run(protocol.String(), func(t *testing.T) {
			wire, err := json.Marshal(protocol.String())
			if err != nil {
				t.Fatal(err)
			}
			target := configuredRateCardTarget()
			target.Protocol = protocol
			parseConfiguredCard(t, rateCardWire(t, "target", "protocol", wire), target)
		})
	}
}

func TestRateCardConfigRejectsInvalidFields(t *testing.T) {
	type fieldCase struct {
		scope, field string
		values       []string
	}
	cases := []fieldCase{
		{"", "version", []string{`null`, `0`, `2`, `"1"`, `true`, `1.0`, `{}`, `[]`}},
		{"", "target", []string{`null`, `[]`, `"target"`, `true`, `1`, `{}`}},
		{"", "card", []string{`null`, `[]`, `"card"`, `true`, `1`, `{}`}},
		{"target", "protocol", []string{`null`, `1`, `"unknown"`, `"Chat_Completions"`, `" chat_completions "`, `"PROTOCOLCHATCOMPLETIONS"`, `"other"`}},
		{"card", "billing", []string{`null`, `1`, `"unknown"`, `"subscription"`, `"unmetered"`, `"TOKEN-METERED"`, `" token-metered "`, `"other"`}},
		{"card", "applicable", []string{`null`, `0`, `"true"`, `[]`, `{}`}},
		{"card", "token_only", []string{`null`, `1`, `"false"`, `[]`, `{}`}},
		{"card", "cache_writes", []string{`null`, `{}`, `"array"`, `1`, `true`, `[null]`, `[[]]`, `[1]`, `[{}]`,
			`[{"lifetime_seconds":0}]`, `[{"lifetime_seconds":-1}]`, `[{"lifetime_seconds":1.5}]`,
			`[{"lifetime_seconds":1.0}]`, `[{"lifetime_seconds":1e3}]`, `[{"lifetime_seconds":"300"}]`,
			`[{"lifetime_seconds":null}]`, `[{"lifetime_seconds":true}]`, `[{"lifetime_seconds":9223372037}]`,
			`[{"lifetime_seconds":9223372036854775808}]`, `[{"lifetime_seconds":1},{"lifetime_seconds":1,"per_million":0}]`,
			`[{"lifetime_seconds":1,"unexpected":0}]`, `[{"lifetime_seconds":1,"lifetime_seconds":2}]`,
			`[{"lifetime_seconds":1,"per_million":-1}]`, `[{"lifetime_seconds":1,"per_million":"0"}]`,
			`[{"lifetime_seconds":1,"per_million":1e-9999}]`}},
	}
	for _, field := range []string{"fresh_input_per_million", "cache_read_per_million", "output_per_million"} {
		cases = append(cases, fieldCase{"card", field, []string{`-1`, `-1e-9999`, `1e9999`, `1e-9999`, `0.00001e-9999`, `"NaN"`, `"Infinity"`, `"0"`, `true`, `[]`, `{}`}})
	}
	for _, scope := range []struct {
		name   string
		fields []string
	}{
		{"target", []string{"provider", "model"}},
		{"card", []string{"id", "revision", "entry", "pricing_provider", "currency"}},
	} {
		for _, field := range scope.fields {
			cases = append(cases, fieldCase{scope.name, field, []string{`null`, `""`, `1`, `true`, `[]`, `{}`, `"a\nb"`, `"a\u0000b"`, `"a\u0085b"`, `"\ud800"`, `"\udc00"`, `"\ud800x"`, `"\ud800\u1234"`, `"` + strings.Repeat("a", 1025) + `"`}})
		}
	}
	for _, tc := range cases {
		t.Run(tc.scope+"/"+tc.field, func(t *testing.T) {
			// Every scalar identity, assertion and protocol is required. Rates
			// and the cache-write collection are the only optional card fields.
			if tc.field != "cache_writes" && !strings.HasSuffix(tc.field, "_per_million") {
				assertInvalidRateCard(t, rateCardWire(t, tc.scope, tc.field, nil))
			}
			for i, value := range tc.values {
				t.Run(strconv.Itoa(i), func(t *testing.T) {
					assertInvalidRateCard(t, rateCardWire(t, tc.scope, tc.field, []byte(value)))
				})
			}
		})
	}
}

func TestRateCardConfigURLBoundary(t *testing.T) {
	for _, raw := range []string{
		`null`, `1`, `true`, `[]`, `{}`, `"ftp://host/v1"`, `"/relative"`, `"host/v1"`, `"https://"`,
		`"https://:443/path"`, `"https://user:password@host/v1"`, `"https://user@host/v1"`,
		`"https://host/v1?key=secret"`, `"https://host/v1?"`, `"https://host/v1#fragment"`, `"https://host/v1#"`,
		`"https://host:bad/v1"`, `"https://ho st/v1"`, `"https://host/\n"`, `"https://host/%xx"`,
		`"` + "https://host/" + strings.Repeat("a", 4096) + `"`,
	} {
		assertInvalidRateCard(t, rateCardWire(t, "target", "base_url", []byte(raw)))
	}
	assertInvalidRateCard(t, rateCardWire(t, "target", "base_url", nil))
	for _, raw := range []string{"", "http://localhost:4000/v1", "https://host/a/../b/%2f/", "https://[::1]:8443/v1"} {
		wire, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		target := configuredRateCardTarget()
		target.BaseURL = raw
		parseConfiguredCard(t, rateCardWire(t, "target", "base_url", wire), target)
	}
}

func assertInvalidRateCard(t *testing.T, data []byte) {
	t.Helper()
	config, err := engine.ParseRateCardConfig(data, time.Time{})
	if !errors.Is(err, engine.ErrInvalidRateCardConfig) {
		t.Fatalf("parse error = %v, want invalid configuration", err)
	}
	if _, err := config.Select(configuredRateCardTarget()); !errors.Is(err, engine.ErrRateCardTargetMismatch) {
		t.Fatalf("invalid parse retained selectable card: %v", err)
	}
}

func TestRateCardConfigStrictJSONAndLimits(t *testing.T) {
	for _, data := range [][]byte{
		nil, []byte(`null`), []byte(`[]`), []byte(`true`), []byte(`1`), []byte(`{`), []byte(`{}`),
		[]byte(rateCardInput + `{}`), []byte(rateCardInput + ` null`), []byte(rateCardInput + ` invalid`),
		[]byte(strings.Replace(rateCardInput, `"version": 1`, `"version": 1,"version": 1`, 1)),
		[]byte(strings.Replace(rateCardInput, `"provider":`, `"provider":"duplicate","provider":`, 1)),
		[]byte(strings.Replace(rateCardInput, `"id":`, `"id":"duplicate","id":`, 1)),
		[]byte(strings.Replace(rateCardInput, `1.25`, `NaN`, 1)),
		[]byte(strings.Replace(rateCardInput, `1.25`, `Infinity`, 1)),
		[]byte(strings.Replace(rateCardInput, `1.25`, `01`, 1)),
		[]byte(strings.Replace(rateCardInput, `local policy`, string([]byte{0xff}), 1)),
		rateCardWire(t, "", "unexpected", []byte(`1`)),
		rateCardWire(t, "target", "unexpected", []byte(`1`)),
		rateCardWire(t, "card", "unexpected", []byte(`1`)),
		bytes.Repeat([]byte(" "), engine.MaxRateCardConfigBytes+1),
	} {
		assertInvalidRateCard(t, data)
	}
	atLimit := append([]byte(rateCardInput), bytes.Repeat([]byte(" "), engine.MaxRateCardConfigBytes-len(rateCardInput))...)
	parseConfiguredCard(t, atLimit, configuredRateCardTarget())
	assertInvalidRateCard(t, append(atLimit, ' '))
	// Opaque IDs preserve valid escape pairs, literal replacement characters,
	// escaped backslashes and IDs at the documented byte boundary.
	for _, value := range []string{`"\ud83d\ude00"`, `"literal�"`, `"\\ud800"`, `"` + strings.Repeat("a", 1024) + `"`} {
		parseConfiguredCard(t, rateCardWire(t, "card", "id", []byte(value)), configuredRateCardTarget())
	}
}

func TestRateCardConfigErrorsDoNotRetainInput(t *testing.T) {
	const canary = "RATE_CARD_PRIVATE_CANARY_password_987"
	inputs := [][]byte{
		rateCardWire(t, "", canary, []byte(`"`+canary+`"`)),
		rateCardWire(t, "target", canary, []byte(`"`+canary+`"`)),
		rateCardWire(t, "card", canary, []byte(`"`+canary+`"`)),
		rateCardWire(t, "target", "base_url", []byte(`"https://user:`+canary+`@host/path?key=`+canary+`"`)),
		rateCardWire(t, "target", "base_url", []byte(`"https://%`+canary+`"`)),
		rateCardWire(t, "target", "protocol", []byte(`"`+canary+`"`)),
		rateCardWire(t, "card", "billing", []byte(`"`+canary+`"`)),
		rateCardWire(t, "card", "fresh_input_per_million", []byte(`"`+canary+`"`)),
		rateCardWire(t, "card", "cache_writes", []byte(`[{"lifetime_seconds":"`+canary+`"}]`)),
		[]byte(`{"` + canary + `":`),
		[]byte(strings.Replace(rateCardInput, `"version": 1`, `"version":"`+canary+`"`, 1)),
		[]byte(strings.Replace(rateCardInput, `"id":"local policy"`, `"id":"`+canary+`\n"`, 1)),
		[]byte(rateCardInput + `{"` + canary + `":true}`),
	}
	for i, input := range inputs {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			_, err := engine.ParseRateCardConfig(input, time.Time{})
			if !errors.Is(err, engine.ErrInvalidRateCardConfig) {
				t.Fatalf("parse error = %v", err)
			}
			for cause := err; cause != nil; cause = errors.Unwrap(cause) {
				if strings.Contains(fmt.Sprintf("%v %+v %#v", cause, cause, cause), canary) {
					t.Fatal("error chain retained private input")
				}
			}
			var syntax *json.SyntaxError
			var scalar *json.UnmarshalTypeError
			if errors.As(err, &syntax) || errors.As(err, &scalar) || errors.Is(err, llm.ErrParseModelProtocol) || errors.Is(err, llm.ErrParseBillingMode) {
				t.Fatal("raw parser cause escaped input boundary")
			}
		})
	}
	config, err := engine.ParseRateCardConfig([]byte(rateCardInput), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	target := configuredRateCardTarget()
	target.Provider, target.Model, target.BaseURL = canary, llm.ModelID(canary), canary
	_, err = config.Select(target)
	if !errors.Is(err, engine.ErrRateCardTargetMismatch) || strings.Contains(fmt.Sprintf("%+v", err), canary) {
		t.Fatalf("selection error = %v", err)
	}
}
