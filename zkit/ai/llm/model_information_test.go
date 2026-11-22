package llm_test

import (
	"encoding/json"
	"errors"
	"maps"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/zarldev/zarlmono/zkit/ai/llm"
)

func TestModelDescriptorAbsentMetadata(t *testing.T) {
	var descriptor llm.ModelDescriptor
	surface := descriptor.Surfaces[llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS]
	if surface.Support != llm.Supports.SUPPORTUNKNOWN {
		t.Fatalf("absent protocol support = %v, want unknown", surface.Support)
	}
	if surface.Capabilities.Tools == llm.Supports.SUPPORTUNSUPPORTED ||
		surface.Capabilities.Tools == llm.Supports.SUPPORTSUPPORTED {
		t.Fatalf("absent tools support = %v, want unknown", surface.Capabilities.Tools)
	}
	if surface.Limits != (llm.ModelLimits{}) {
		t.Fatalf("absent limits = %+v, want zero", surface.Limits)
	}
	if descriptor.Evidence != nil || descriptor.RateCard != (llm.RateCardReference{}) {
		t.Fatalf("absent metadata acquired evidence or pricing: %+v", descriptor)
	}
	var provenance llm.MetadataProvenance
	if provenance.Source != llm.MetadataSources.METADATAUNKNOWN ||
		!provenance.ObservedAt.IsZero() || !provenance.ExpiresAt.IsZero() {
		t.Fatalf("absent provenance = %+v, want unknown with no timestamps", provenance)
	}
	clone := descriptor.Clone()
	if clone.Surfaces != nil || clone.Evidence != nil {
		t.Fatal("cloning absent metadata allocated collections")
	}
}

func TestModelDescriptorOpaqueIdentity(t *testing.T) {
	for _, alias := range []llm.ModelID{"Team/FAST:v2", "claude-gpt-vision-thinking", "deployment name with spaces"} {
		t.Run(string(alias), func(t *testing.T) {
			descriptor := llm.ModelDescriptor{
				Route:          "private-gateway",
				RequestedModel: alias,
			}
			clone := descriptor.Clone()
			if clone.RequestedModel != alias || clone.Route != descriptor.Route {
				t.Fatalf("identity changed: %+v", clone)
			}
			if clone.CanonicalModel != "" || clone.ServingProvider != "" || clone.Deployment != "" ||
				len(clone.Surfaces) != 0 || clone.RateCard != (llm.RateCardReference{}) {
				t.Fatalf("alias spelling inferred resolved metadata: %+v", clone)
			}
		})
	}
}

func TestModelDescriptorProtocolAndModalityDirections(t *testing.T) {
	descriptor := llm.ModelDescriptor{
		Route:           "gateway-west",
		RequestedModel:  "fast",
		CanonicalModel:  "vendor/model-2026",
		ServingProvider: "serving-service",
		Deployment:      "deployment-west-4",
		Surfaces: map[llm.ModelProtocol]llm.ModelSurface{
			llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS: {
				Support: llm.Supports.SUPPORTSUPPORTED,
				Capabilities: llm.EffectiveCapabilities{
					Tools: llm.Supports.SUPPORTUNSUPPORTED,
				},
				Modalities: llm.DirectionalModalities{
					Input:  llm.ModalitySupport{Image: llm.Supports.SUPPORTSUPPORTED},
					Output: llm.ModalitySupport{Image: llm.Supports.SUPPORTUNSUPPORTED},
				},
				Limits: llm.ModelLimits{InputTokens: 32000, OutputTokens: 4000, ContextTokens: 32768},
			},
			llm.ModelProtocols.PROTOCOLRESPONSES: {Support: llm.Supports.SUPPORTUNSUPPORTED},
		},
	}
	chat := descriptor.Surfaces[llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS]
	responses := descriptor.Surfaces[llm.ModelProtocols.PROTOCOLRESPONSES]
	messages := descriptor.Surfaces[llm.ModelProtocols.PROTOCOLMESSAGES]
	if chat.Support != llm.Supports.SUPPORTSUPPORTED || responses.Support != llm.Supports.SUPPORTUNSUPPORTED ||
		messages.Support != llm.Supports.SUPPORTUNKNOWN {
		t.Fatal("supported, unsupported and unknown protocols were conflated")
	}
	if chat.Capabilities.Tools != llm.Supports.SUPPORTUNSUPPORTED || chat.Capabilities.Streaming != llm.Supports.SUPPORTUNKNOWN {
		t.Fatal("protocol support implied a feature assertion")
	}
	if chat.Modalities.Input.Image != llm.Supports.SUPPORTSUPPORTED ||
		chat.Modalities.Output.Image != llm.Supports.SUPPORTUNSUPPORTED ||
		chat.Modalities.Input.Audio != llm.Supports.SUPPORTUNKNOWN {
		t.Fatalf("directional modalities conflated: %+v", chat.Modalities)
	}
	if chat.Limits.InputTokens != 32000 || chat.Limits.OutputTokens != 4000 || chat.Limits.ContextTokens != 32768 ||
		messages.Limits != (llm.ModelLimits{}) {
		t.Fatal("independent protocol limits changed or were inferred")
	}
}

func TestModelDescriptorCloneOwnsCollections(t *testing.T) {
	observed := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	descriptor := llm.ModelDescriptor{
		Surfaces: map[llm.ModelProtocol]llm.ModelSurface{
			llm.ModelProtocols.PROTOCOLRESPONSES: {Support: llm.Supports.SUPPORTSUPPORTED},
		},
		Evidence: []llm.MetadataProvenance{{
			Source: llm.MetadataSources.METADATADISCOVERY, Snapshot: "gateway-snapshot", Revision: "rev-4",
			ObservedAt: observed, ExpiresAt: observed.Add(time.Hour),
		}},
		RateCard: llm.RateCardReference{ID: "vendor-rates", Revision: "2026-11", Entry: "deployment-tier"},
	}
	clone := descriptor.Clone()
	if !maps.Equal(clone.Surfaces, descriptor.Surfaces) || !slices.Equal(clone.Evidence, descriptor.Evidence) ||
		clone.RateCard != descriptor.RateCard {
		t.Fatal("clone changed metadata before either snapshot was mutated")
	}
	clone.Surfaces[llm.ModelProtocols.PROTOCOLRESPONSES] = llm.ModelSurface{Support: llm.Supports.SUPPORTUNSUPPORTED}
	clone.Evidence[0].Snapshot = "changed"
	clone.RateCard.Entry = "other-tier"
	if descriptor.Surfaces[llm.ModelProtocols.PROTOCOLRESPONSES].Support != llm.Supports.SUPPORTSUPPORTED ||
		descriptor.Evidence[0].Snapshot != "gateway-snapshot" || descriptor.RateCard.Entry != "deployment-tier" {
		t.Fatal("clone mutations reached the original snapshot")
	}
	delete(descriptor.Surfaces, llm.ModelProtocols.PROTOCOLRESPONSES)
	descriptor.Evidence[0].Revision = "rev-5"
	if len(clone.Surfaces) != 1 || clone.Evidence[0].Revision != "rev-4" ||
		!clone.Evidence[0].ObservedAt.Equal(observed) || !clone.Evidence[0].ExpiresAt.Equal(observed.Add(time.Hour)) {
		t.Fatal("original mutations reached the clone or freshness evidence changed")
	}
}

func TestModelDescriptorEvidenceDoesNotGrantSupport(t *testing.T) {
	observed := time.Date(2026, time.November, 11, 12, 0, 0, 0, time.UTC)
	descriptor := llm.ModelDescriptor{
		Route: "gateway-west", RequestedModel: "vision-thinking-alias",
		Evidence: []llm.MetadataProvenance{
			{Source: llm.MetadataSources.METADATACATALOGUE, Snapshot: "catalogue-snapshot", Revision: "rev-1"},
			{Source: llm.MetadataSources.METADATADISCOVERY, Snapshot: "discovery-snapshot", Revision: "rev-2",
				ObservedAt: observed, ExpiresAt: observed.Add(time.Hour)},
			{Source: llm.MetadataSources.METADATACONFIGURATION, Snapshot: "config-snapshot", Revision: "rev-3"},
		},
		RateCard: llm.RateCardReference{ID: "rate-card", Revision: "2026-11", Entry: "opaque-entry"},
	}
	clone := descriptor.Clone()
	if clone.Surfaces != nil || clone.CanonicalModel != "" || clone.ServingProvider != "" || clone.Deployment != "" {
		t.Fatalf("evidence or pricing inferred resolved identity or support: %+v", clone)
	}
	if !slices.Equal(clone.Evidence, descriptor.Evidence) || clone.RateCard != descriptor.RateCard ||
		clone.Route != descriptor.Route || clone.RequestedModel != descriptor.RequestedModel {
		t.Fatal("clone changed provenance, freshness, pricing reference or requested identity")
	}
}

func TestModelSupportParsing(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  llm.Support
	}{
		{input: "unknown", want: llm.Supports.SUPPORTUNKNOWN},
		{input: "supported", want: llm.Supports.SUPPORTSUPPORTED},
		{input: "unsupported", want: llm.Supports.SUPPORTUNSUPPORTED},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := llm.ParseSupport(tc.input)
			if err != nil || got != tc.want || got.String() != tc.input || !got.IsValid() {
				t.Fatalf("ParseSupport(%q) = %v, %v; want %v", tc.input, got, err, tc.want)
			}
		})
	}
	for _, input := range []any{"", "SUPPORTED", "yes", -1, 99} {
		got, err := llm.ParseSupport(input)
		if !errors.Is(err, llm.ErrParseSupport) || got.IsValid() {
			t.Errorf("ParseSupport(%v) = %v, %v; want invalid with ErrParseSupport", input, got, err)
		}
	}
	if zero := (llm.Support{}); zero != llm.Supports.SUPPORTUNKNOWN || !zero.IsValid() {
		t.Fatalf("zero support = %v, want valid unknown", zero)
	}
}

func TestModelProtocolParsing(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  llm.ModelProtocol
	}{
		{input: "unknown", want: llm.ModelProtocols.PROTOCOLUNKNOWN},
		{input: "chat_completions", want: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS},
		{input: "responses", want: llm.ModelProtocols.PROTOCOLRESPONSES},
		{input: "messages", want: llm.ModelProtocols.PROTOCOLMESSAGES},
		{input: "generate_content", want: llm.ModelProtocols.PROTOCOLGENERATECONTENT},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := llm.ParseModelProtocol(tc.input)
			if err != nil || got != tc.want || got.String() != tc.input || !got.IsValid() {
				t.Fatalf("ParseModelProtocol(%q) = %v, %v; want %v", tc.input, got, err, tc.want)
			}
		})
	}
	for _, input := range []any{"", "Responses", "/v1/messages", -1, 99} {
		got, err := llm.ParseModelProtocol(input)
		if !errors.Is(err, llm.ErrParseModelProtocol) || got.IsValid() {
			t.Errorf("ParseModelProtocol(%v) = %v, %v; want invalid with ErrParseModelProtocol", input, got, err)
		}
	}
}

func TestModelMetadataSourceParsing(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  llm.MetadataSource
	}{
		{input: "unknown", want: llm.MetadataSources.METADATAUNKNOWN},
		{input: "catalogue", want: llm.MetadataSources.METADATACATALOGUE},
		{input: "discovery", want: llm.MetadataSources.METADATADISCOVERY},
		{input: "configuration", want: llm.MetadataSources.METADATACONFIGURATION},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := llm.ParseMetadataSource(tc.input)
			if err != nil || got != tc.want || got.String() != tc.input || !got.IsValid() {
				t.Fatalf("ParseMetadataSource(%q) = %v, %v; want %v", tc.input, got, err, tc.want)
			}
		})
	}
	for _, input := range []any{"", "Discovery", "gateway-json", -1, 99} {
		got, err := llm.ParseMetadataSource(input)
		if !errors.Is(err, llm.ErrParseMetadataSource) || got.IsValid() {
			t.Errorf("ParseMetadataSource(%v) = %v, %v; want invalid with ErrParseMetadataSource", input, got, err)
		}
	}
}

func TestModelEnumCodecsAndNumericValues(t *testing.T) {
	t.Run("support", func(t *testing.T) {
		testModelEnumCodecs[llm.Support](t, []modelEnumCase[llm.Support]{
			{value: llm.Supports.SUPPORTUNKNOWN, number: 0},
			{value: llm.Supports.SUPPORTSUPPORTED, number: 1},
			{value: llm.Supports.SUPPORTUNSUPPORTED, number: 2},
		}, llm.ParseSupport, llm.ErrParseSupport)
	})
	t.Run("protocol", func(t *testing.T) {
		testModelEnumCodecs[llm.ModelProtocol](t, []modelEnumCase[llm.ModelProtocol]{
			{value: llm.ModelProtocols.PROTOCOLUNKNOWN, number: 0},
			{value: llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS, number: 1},
			{value: llm.ModelProtocols.PROTOCOLRESPONSES, number: 2},
			{value: llm.ModelProtocols.PROTOCOLMESSAGES, number: 3},
			{value: llm.ModelProtocols.PROTOCOLGENERATECONTENT, number: 4},
		}, llm.ParseModelProtocol, llm.ErrParseModelProtocol)
	})
	t.Run("metadata", func(t *testing.T) {
		testModelEnumCodecs[llm.MetadataSource](t, []modelEnumCase[llm.MetadataSource]{
			{value: llm.MetadataSources.METADATAUNKNOWN, number: 0},
			{value: llm.MetadataSources.METADATACATALOGUE, number: 1},
			{value: llm.MetadataSources.METADATADISCOVERY, number: 2},
			{value: llm.MetadataSources.METADATACONFIGURATION, number: 3},
		}, llm.ParseMetadataSource, llm.ErrParseMetadataSource)
	})
}

func TestModelProtocolJSONMapKeys(t *testing.T) {
	// A protocol-indexed support snapshot exercises JSON's Text codec for keys
	// and JSON codec for values without making ModelDescriptor a wire schema.
	want := map[llm.ModelProtocol]llm.Support{
		llm.ModelProtocols.PROTOCOLUNKNOWN:         llm.Supports.SUPPORTUNKNOWN,
		llm.ModelProtocols.PROTOCOLCHATCOMPLETIONS: llm.Supports.SUPPORTSUPPORTED,
		llm.ModelProtocols.PROTOCOLRESPONSES:       llm.Supports.SUPPORTUNSUPPORTED,
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal protocol support: %v", err)
	}
	var wire map[string]string
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("read protocol keys: %v", err)
	}
	if !maps.Equal(wire, map[string]string{
		"unknown": "unknown", "chat_completions": "supported", "responses": "unsupported",
	}) {
		t.Fatalf("protocol support JSON = %s, want canonical keys and support values", encoded)
	}
	var decoded map[llm.ModelProtocol]llm.Support
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal protocol support: %v", err)
	}
	if !maps.Equal(decoded, want) {
		t.Fatalf("protocol support round-trip = %v, want %v", decoded, want)
	}
	if _, exists := decoded[llm.ModelProtocols.PROTOCOLMESSAGES]; exists {
		t.Fatal("round-trip added an absent protocol")
	}
	if err := json.Unmarshal([]byte(`{"Responses":"supported"}`), &decoded); !errors.Is(err, llm.ErrParseModelProtocol) {
		t.Fatalf("noncanonical protocol key error = %v, want ErrParseModelProtocol", err)
	}
}

type modelEnumValue interface {
	comparable
	String() string
	IsValid() bool
	MarshalText() ([]byte, error)
	MarshalBinary() ([]byte, error)
}

type modelEnumCase[E modelEnumValue] struct {
	value  E
	number int
}

func testModelEnumCodecs[E modelEnumValue, P interface {
	*E
	UnmarshalText([]byte) error
	UnmarshalBinary([]byte) error
}](t *testing.T, cases []modelEnumCase[E], parse func(any) (E, error), parseErr error) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.value.String(), func(t *testing.T) {
			// Numeric parsing uses the declared Go constant value, not the
			// one-based ordinal used by the older, unadopted generator output.
			for _, input := range []any{tc.number, int64(tc.number), uint64(tc.number), float64(tc.number), tc.value, []byte(tc.value.String())} {
				got, err := parse(input)
				if err != nil || got != tc.value {
					t.Fatalf("parse(%v [%T]) = %v, %v; want %v", input, input, got, err, tc.value)
				}
			}
			decoded := cases[len(cases)-1].value
			text, err := tc.value.MarshalText()
			if err != nil || string(text) != tc.value.String() {
				t.Fatalf("MarshalText = %q, %v; want canonical name %q", text, err, tc.value.String())
			}
			if err := P(&decoded).UnmarshalText(text); err != nil || decoded != tc.value {
				t.Fatalf("text round-trip = %v, %v; want %v", decoded, err, tc.value)
			}
			binary, err := tc.value.MarshalBinary()
			if err != nil || string(binary) != tc.value.String() {
				t.Fatalf("MarshalBinary = %q, %v; want raw canonical name %q", binary, err, tc.value.String())
			}
			decoded = cases[len(cases)-1].value
			if err := P(&decoded).UnmarshalBinary(binary); err != nil || decoded != tc.value {
				t.Fatalf("binary round-trip = %v, %v; want %v", decoded, err, tc.value)
			}
			// Older binary output used literal outer quotes. Decoding it is
			// compatible, including the deliberately valid zero/unknown value.
			legacy := []byte(`"` + tc.value.String() + `"`)

			decoded = cases[len(cases)-1].value
			if err := P(&decoded).UnmarshalBinary(legacy); err != nil || decoded != tc.value {
				t.Fatalf("legacy binary decode = %v, %v; want %v", decoded, err, tc.value)
			}
			for _, input := range [][]byte{[]byte("not-a-model-enum"), []byte(`"not-a-model-enum"`)} {
				if err := P(&decoded).UnmarshalText(input); !errors.Is(err, parseErr) || decoded != tc.value {
					t.Fatalf("invalid text changed receiver or lost parse identity: %v, %v", decoded, err)
				}
				if err := P(&decoded).UnmarshalBinary(input); !errors.Is(err, parseErr) || decoded != tc.value {
					t.Fatalf("invalid binary changed receiver or lost parse identity: %v, %v", decoded, err)
				}
			}
		})
	}
	invalid, err := parse(-1)
	if !errors.Is(err, parseErr) || invalid.IsValid() {
		t.Fatalf("invalid numeric parse = %v, %v; want invalid with parse identity", invalid, err)
	}
	// Exercise the local fixes for same-typed invalid and unsupported inputs,
	// as well as fresh-main's rejection of nonintegral/out-of-range numbers.
	for _, input := range []any{invalid, nil, true, struct{}{}, 0.5, math.NaN(), math.Inf(1), uint64(math.MaxUint64)} {
		got, err := parse(input)
		if !errors.Is(err, parseErr) || got.IsValid() {
			t.Errorf("parse(%v [%T]) = %v, %v; want invalid with parse identity", input, input, got, err)
		}
	}
}
