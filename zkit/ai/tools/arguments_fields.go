package tools

import (
	"cmp"
	"encoding"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// argumentField carries only the encoding/json field-selection facts needed
// for integer token preparation. Index paths establish dominance and the first
// folded-name match; they are never used to access destination values.
type argumentField struct {
	name   string
	typ    reflect.Type
	index  []int
	tagged bool
	quoted bool
	strict bool
}

// argumentClassicFields follows encoding/json's breadth-first anonymous promotion,
// same-name dominance, then declaration-index ordering. In particular, two
// equally dominant fields annihilate instead of preparing an ignored subtree.
func argumentClassicFields(root reflect.Type) []argumentField {
	next := []argumentField{{typ: root}}
	var counts map[reflect.Type]int
	nextCounts := map[reflect.Type]int{}
	visited := map[reflect.Type]bool{}
	var fields []argumentField
	for len(next) > 0 {
		current := next
		next = nil
		counts, nextCounts = nextCounts, map[reflect.Type]int{}
		for _, parent := range current {
			if visited[parent.typ] {
				continue
			}
			visited[parent.typ] = true
			for i := range parent.typ.NumField() {
				sf := parent.typ.Field(i)
				ft := sf.Type
				if sf.Anonymous {
					if ft.Kind() == reflect.Pointer {
						ft = ft.Elem()
					}
					if !sf.IsExported() && ft.Kind() != reflect.Struct {
						continue
					}
				} else if !sf.IsExported() {
					continue
				}
				tag := sf.Tag.Get("json")
				if tag == "-" {
					continue
				}
				name, options, _ := strings.Cut(tag, ",")
				if !argumentValidTag(name) {
					name = ""
				}
				ft = sf.Type
				if ft.Name() == "" && ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				index := append(slices.Clone(parent.index), i)
				if name != "" || !sf.Anonymous || ft.Kind() != reflect.Struct {
					field := argumentField{name: name, typ: sf.Type, index: index, tagged: name != ""}
					if name == "" {
						field.name = sf.Name
					}
					for option := range strings.SplitSeq(options, ",") {
						if option == "string" {
							switch ft.Kind() {
							case reflect.Bool, reflect.String, reflect.Float32, reflect.Float64,
								reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
								reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
								field.quoted = true
							}
						}
					}
					fields = append(fields, field)
					if counts[parent.typ] > 1 {
						fields = append(fields, field)
					}
					continue
				}
				nextCounts[ft]++
				if nextCounts[ft] == 1 {
					next = append(next, argumentField{typ: ft, index: index})
				}
			}
		}
	}
	slices.SortFunc(fields, func(a, b argumentField) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tagged != b.tagged {
			if a.tagged {
				return -1
			}
			return 1
		}
		return slices.Compare(a.index, b.index)
	})
	selected := fields[:0]
	for i := 0; i < len(fields); {
		end := i + 1
		for end < len(fields) && fields[end].name == fields[i].name {
			end++
		}
		first := fields[i]
		if end == i+1 || len(first.index) != len(fields[i+1].index) || first.tagged != fields[i+1].tagged {
			selected = append(selected, first)
		}
		i = end
	}
	slices.SortFunc(selected, func(a, b argumentField) int { return slices.Compare(a.index, b.index) })
	return selected
}

func argumentValidTag(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c) && !unicode.IsLetter(c) && !unicode.IsDigit(c) {
			return false
		}
	}
	return true
}

// Fields retain backend-specific folded precedence after exact-name selection.
func argumentFieldType(metadata argumentStruct, key string) reflect.Type {
	fields := metadata.fields
	for _, field := range fields {
		if field.name == key {
			if field.quoted {
				return nil
			}
			return field.typ
		}
	}
	for _, field := range fields {
		if !field.strict && strings.EqualFold(field.name, key) {
			if field.quoted {
				return nil
			}
			return field.typ
		}
	}
	return metadata.fallback
}

// argumentStruct describes only destinations selected by the active ordinary
// encoding/json backend. A format error makes an object opaque: that backend
// rejects the struct before visiting any members, including custom decoders.
type argumentStruct struct {
	fields   []argumentField
	fallback reflect.Type
	opaque   bool
}

func argumentFields(root reflect.Type) argumentStruct {
	if !argumentJSONv2 {
		return argumentStruct{fields: argumentClassicFields(root)}
	}
	return argumentV2Fields(root)
}

// argumentV2Fields mirrors Go 1.27 encoding/json/v2.makeStructFields under
// DefaultOptionsV1: structural/tag errors retain best-effort metadata, exact
// dominance precedes eligible breadth-first folded matches, and only a unique
// shallowest embedded fallback receives unmatched members. No values or custom
// methods are evaluated while computing these facts.
func argumentV2Fields(root reflect.Type) argumentStruct {
	type entry struct {
		typ      reflect.Type
		index    []int
		children bool
	}
	queue := []entry{{typ: root, children: true}}
	seen := map[reflect.Type]bool{root: true}
	var fields, fallbacks []argumentField
	var out argumentStruct
	for q := 0; q < len(queue); q++ {
		parent := queue[q]
		for i := range parent.typ.NumField() {
			sf := parent.typ.Field(i)
			opts, ignored := argumentV2Options(sf)
			if ignored {
				continue
			}
			ft := argumentIndirectType(sf.Type)
			if sf.Anonymous && !opts.named && ft.Kind() == reflect.Struct {
				opts.embed = true
			}
			field := argumentField{name: opts.name, typ: sf.Type, index: append(slices.Clone(parent.index), i), tagged: opts.named, strict: opts.strict && !opts.ignore}
			if opts.embed {
				// Invalid embed+options with an explicit name falls back to a regular
				// field. Without a name the shim clears other options and still embeds.
				if opts.named || opts.strict || opts.ignore || opts.omitzero || opts.omitempty || opts.quoted || opts.format != "" {
					if opts.named {
						opts.embed = false
					} else {
						opts = argumentFieldOptions{name: opts.name, embed: true}
						field.strict = false
					}
				}
				if opts.embed {
					if ft.Kind() == reflect.Struct {
						if parent.children {
							queue = append(queue, entry{ft, field.index, !seen[ft]})
						}
						seen[ft] = true
						continue
					}
					if !sf.IsExported() {
						continue
					}
					if ft == argumentRawFallbackType {
						fallbacks = append(fallbacks, field)
						continue
					}
					if ft.Kind() == reflect.Map && ft.Key().Kind() == reflect.String && !argumentAnyMethods(ft.Key()) {
						field.typ = ft.Elem()
						fallbacks = append(fallbacks, field)
						continue
					}
					// Invalid non-struct/non-string-map embed retains an ordinary field.
				}
			}
			if !sf.IsExported() {
				if !sf.Anonymous || ft.Kind() != reflect.Struct || argumentAnyMethods(ft) || (opts.omitzero && argumentIsZeroer(ft)) {
					continue
				}
			}
			field.quoted = opts.quoted && argumentStringPrimitive(sf.Type)
			fields = append(fields, field)
			out.opaque = out.opaque || opts.format != ""
		}
	}
	// Stable sorting preserves breadth-first order among equally dominant fields.
	slices.SortStableFunc(fields, func(a, b argumentField) int {
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		if a.tagged != b.tagged {
			if a.tagged {
				return -1
			}
			return 1
		}
		return 0
	})
	selected := fields[:0]
	for i := 0; i < len(fields); {
		end := i + 1
		for end < len(fields) && fields[end].name == fields[i].name {
			end++
		}
		first := fields[i]
		if end == i+1 || len(first.index) != len(fields[i+1].index) || first.tagged != fields[i+1].tagged {
			selected = append(selected, first)
		}
		i = end
	}
	slices.SortFunc(selected, func(a, b argumentField) int {
		if c := cmp.Compare(len(a.index), len(b.index)); c != 0 {
			return c
		}
		return slices.Compare(a.index, b.index)
	})
	out.fields = selected
	if len(fallbacks) == 1 || (len(fallbacks) > 1 && len(fallbacks[0].index) != len(fallbacks[1].index)) {
		if ft := argumentIndirectType(fallbacks[0].typ); ft != argumentRawFallbackType {
			out.fallback = fallbacks[0].typ
		}
	}
	return out
}

func argumentIndirectType(t reflect.Type) reflect.Type {
	if t.Kind() == reflect.Pointer && t.Name() == "" {
		return t.Elem()
	}
	return t
}

func argumentStringPrimitive(t reflect.Type) bool {
	t = argumentIndirectType(t)
	switch t.Kind() {
	case reflect.Bool, reflect.String, reflect.Float32, reflect.Float64,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	}
	return false
}

func argumentAnyMethods(t reflect.Type) bool {
	return argumentCustomDecoder(t) || argumentStreamingMarshaler(t) ||
		t.Implements(reflect.TypeFor[json.Marshaler]()) || reflect.PointerTo(t).Implements(reflect.TypeFor[json.Marshaler]()) ||
		t.Implements(reflect.TypeFor[encoding.TextMarshaler]()) || reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextMarshaler]()) ||
		t.Implements(reflect.TypeFor[encoding.TextAppender]()) || reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextAppender]())
}

func argumentIsZeroer(t reflect.Type) bool {
	method := reflect.TypeFor[interface{ IsZero() bool }]()
	return t.Implements(method) || reflect.PointerTo(t).Implements(method)
}

type argumentFieldOptions struct {
	name                                                      string
	named, embed, strict, ignore, omitzero, omitempty, quoted bool
	format                                                    string
}

// argumentV2Options retains the selected backend's best-effort parse, not the
// classic comma-split grammar. Only format values permit single-quoted tokens.
// In particular, a quoted comma cannot introduce a case or string option.
func argumentV2Options(sf reflect.StructField) (argumentFieldOptions, bool) {
	var out argumentFieldOptions
	tag := sf.Tag.Get("json")
	if tag == "-" || (!sf.IsExported() && !sf.Anonymous) {
		return out, true
	}
	out.name = sf.Name
	if len(tag) > 0 && tag[0] != ',' {
		n := len(tag) - len(strings.TrimLeftFunc(tag, func(r rune) bool { return !strings.ContainsRune(",\\'\"`", r) }))
		name := tag[:n]
		valid := true
		if !strings.HasPrefix(tag[n:], ",") && len(name) != len(tag) {
			name, n, valid = argumentTagOption(tag, false)
		}
		if valid {
			out.name = string([]rune(name))
			out.named = true
		}
		tag = tag[n:]
	}
	for len(tag) > 0 {
		if tag[0] == ',' {
			tag = tag[1:]
			if len(tag) == 0 {
				break
			}
		}
		opt, n, _ := argumentTagOption(tag, false)
		tag = tag[n:]
		switch opt {
		case "case":
			if !strings.HasPrefix(tag, ":") {
				break
			}
			tag = tag[1:]
			value, n, valid := argumentTagOption(tag, false)
			if !valid {
				break
			}
			tag = tag[n:]
			switch value {
			case "strict":
				out.strict = true
			case "ignore":
				out.ignore = true
			}
		case "embed":
			out.embed = true
		case "omitzero":
			out.omitzero = true
		case "omitempty":
			out.omitempty = true
		case "string":
			out.quoted = true
		case "format":
			if !strings.HasPrefix(tag, ":") {
				break
			}
			tag = tag[1:]
			value, n, valid := argumentTagOption(tag, true)
			if !valid || value == "" {
				break
			}
			tag = tag[n:]
			out.format = value
		}
	}
	return out, false
}

// argumentTagOption uses the token consumption of Go 1.27 consumeTagOption.
// Errors are represented only by validity: the final ordinary decoder owns all
// diagnostics and the shim suppresses tag errors while retaining parsed facts.
func argumentTagOption(in string, quoted bool) (string, int, bool) {
	i := strings.IndexByte(in, ',')
	if i < 0 {
		i = len(in)
	}
	r, _ := utf8.DecodeRuneInString(in)
	if r == '_' || unicode.IsLetter(r) {
		n := len(in) - len(strings.TrimLeftFunc(in, func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) }))
		return in[:n], n, true
	}
	if r == '\'' && quoted {
		escaped := false
		b := []byte{'"'}
		for n := 1; n < len(in); {
			r, size := utf8.DecodeRuneInString(in[n:])
			switch {
			case escaped:
				if r == '\'' {
					b = b[:len(b)-1]
				}
				escaped = false
			case r == '\\':
				escaped = true
			case r == '"':
				b = append(b, '\\')
			case r == '\'':
				b = append(b, '"')
				value, err := strconv.Unquote(string(b))
				if err != nil {
					return in[:i], i, false
				}
				return value, n + 1, true
			}
			b = append(b, in[n:n+size]...)
			n += size
		}
	}
	return in[:i], i, false
}
