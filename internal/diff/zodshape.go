package diff

import (
	"regexp"
	"slices"
	"strings"
)

// A zod schema is a value, and the type its users see is read off it with z.infer. What
// a change to the schema does to that type is in the schema's text: which keys of
// z.object came, went or changed, and whether a key is required in the output type.
// `.optional()` leaves a key optional on both sides of a parse; `.default(…)` makes it
// optional on input and required on output, which is what breaks a literal of the type.

// Kinds of ZodKeyChange.
const (
	ZodAdded       = "added"
	ZodRemoved     = "removed"
	ZodRetyped     = "retyped"
	ZodNowOptional = "now-optional"
	ZodNowRequired = "now-required"
)

// maxZodDepth bounds how many schemas a base or a helper is followed through.
const maxZodDepth = 4

// ZodKeyChange is what a change did to one key of an object schema.
type ZodKeyChange struct {
	Key  string
	Kind string
	// Required says whether the key has to be there in the output type after the
	// change; Default is the argument of `.default(…)`, if that is why.
	Required bool
	Default  string
	// Before and After are the key's schema without validators, for a retyped key.
	Before, After string
	// Wider means the new schema holds the old one: what was written still fits, what
	// is read may be more than before.
	Wider bool
}

// ZodChange is what a change did to a schema: to the keys of an object, or the members
// of an enum or union.
type ZodChange struct {
	Shape string // object, enum, union
	Keys  []ZodKeyChange
	// Added and Removed are the members of an enum or union that came and went.
	Added, Removed []string
	// Exact means the schema was read to the end on both sides. If not, Unread says
	// what stopped it, and nothing else here is filled in.
	Exact  bool
	Unread string
}

// Lines says the change the way a contract line does: `+ custom (required in output,
// default [])`, `- nick`, `~ kind: z.enum(A) -> z.union([…]) (wider)`.
func (c ZodChange) Lines() []string {
	var lines []string
	for _, k := range c.Keys {
		switch k.Kind {
		case ZodAdded:
			switch {
			case k.Default != "":
				lines = append(lines, "+ "+k.Key+" (required in output, default "+k.Default+")")
			case k.Required:
				lines = append(lines, "+ "+k.Key+" (required)")
			default:
				lines = append(lines, "+ "+k.Key+" (optional)")
			}
		case ZodRetyped:
			line := "~ " + k.Key + ": " + k.Before + " -> " + k.After
			if k.Wider {
				line += " (wider)"
			}
			lines = append(lines, line)
		case ZodNowOptional:
			lines = append(lines, "~ "+k.Key+": now optional")
		case ZodNowRequired:
			lines = append(lines, "~ "+k.Key+": now required")
		}
	}
	for _, m := range c.Added {
		lines = append(lines, "+ "+m)
	}
	for _, k := range c.Keys {
		if k.Kind == ZodRemoved {
			lines = append(lines, "- "+k.Key)
		}
	}
	for _, m := range c.Removed {
		lines = append(lines, "- "+m)
	}
	return lines
}

// LiteralKeys returns the keys a literal of the output type has to set now and didn't
// before: added as required, or no longer optional.
func (c ZodChange) LiteralKeys() []string {
	var keys []string
	for _, k := range c.Keys {
		if (k.Kind == ZodAdded && k.Required) || k.Kind == ZodNowRequired {
			keys = append(keys, k.Key)
		}
	}
	return keys
}

// MembersAdded and MembersRemoved say whether an enum or union gained or lost a member.
func (c ZodChange) MembersAdded() bool   { return len(c.Added) > 0 }
func (c ZodChange) MembersRemoved() bool { return len(c.Removed) > 0 }

// Empty says whether the type came out the same: only validators changed.
func (c ZodChange) Empty() bool {
	return c.Exact && len(c.Keys) == 0 && len(c.Added) == 0 && len(c.Removed) == 0
}

// ZodSchemaChange compares the schema a file declares under name on both sides of a
// diff. False means the name isn't a zod schema on both sides. A schema the reader
// can't follow to the end, such as one with `.transform(…)` or a base from another
// file, comes back with Exact false and the reason.
func ZodSchemaChange(oldLines, newLines []string, name string) (ZodChange, bool) {
	before, okBefore := (&zodFile{lines: oldLines}).shape(name, 0)
	after, okAfter := (&zodFile{lines: newLines}).shape(name, 0)
	if !okBefore || !okAfter {
		return ZodChange{}, false
	}
	change := ZodChange{Shape: after.kind}
	switch {
	case after.unread != "":
		change.Unread = after.unread
	case before.unread != "":
		change.Unread = before.unread
	case before.kind != after.kind:
		change.Unread = "it was " + before.kind + " and is " + after.kind
	}
	if change.Unread != "" {
		return change, true
	}
	change.Exact = true

	old := make(map[string]zodKey, len(before.keys))
	for _, k := range before.keys {
		old[k.name] = k
	}
	kept := make(map[string]bool, len(after.keys))
	for _, k := range after.keys {
		kept[k.name] = true
		was, existed := old[k.name]
		switch {
		case !existed:
			change.Keys = append(change.Keys, ZodKeyChange{Key: k.name, Kind: ZodAdded, Required: !k.optional, Default: k.def})
		case was.core != k.core:
			change.Keys = append(change.Keys, ZodKeyChange{
				Key: k.name, Kind: ZodRetyped, Required: !k.optional, Default: k.def,
				Before: was.show, After: k.show, Wider: strings.Contains(k.core, was.core),
			})
		case was.optional && !k.optional:
			change.Keys = append(change.Keys, ZodKeyChange{Key: k.name, Kind: ZodNowRequired, Required: true, Default: k.def})
		case !was.optional && k.optional:
			change.Keys = append(change.Keys, ZodKeyChange{Key: k.name, Kind: ZodNowOptional})
		}
	}
	for _, k := range before.keys {
		if !kept[k.name] {
			change.Keys = append(change.Keys, ZodKeyChange{Key: k.name, Kind: ZodRemoved})
		}
	}
	for _, m := range after.members {
		if !slices.Contains(before.members, m) {
			change.Added = append(change.Added, m)
		}
	}
	for _, m := range before.members {
		if !slices.Contains(after.members, m) {
			change.Removed = append(change.Removed, m)
		}
	}
	return change, true
}

// ZodKeyLine returns the line on which the schema a file declares under name writes
// the given key, or 0: the key comes from a base, or isn't there.
func ZodKeyLine(lines []string, name, key string) int {
	_, first, last, ok := tsDeclaredValue(lines, name)
	if !ok {
		return 0
	}
	return TSLiteralKeyLine(lines, first, last, key)
}

type zodKey struct {
	name string
	core string // The key's schema without validators and whitespace, to compare
	show string // The same, readable
	// optional is about the output type: `.optional()` without a default after it.
	optional bool
	def      string
}

type zodShape struct {
	kind    string // object, enum, union, other
	keys    []zodKey
	members []string
	unread  string // What stopped the reader, if anything did
}

type zodFile struct {
	lines []string
}

// shape reads the schema declared under name.
func (f *zodFile) shape(name string, depth int) (zodShape, bool) {
	expr, _, _, ok := tsDeclaredValue(f.lines, name)
	if !ok {
		return zodShape{}, false
	}
	return f.shapeOf(expr, depth)
}

func (f *zodFile) shapeOf(expr string, depth int) (zodShape, bool) {
	segs, ok := zodChain(expr)
	if !ok || len(segs) == 0 {
		return zodShape{}, false
	}
	var s zodShape
	rest := segs[1:]
	switch {
	case segs[0].name == "z" && !segs[0].called:
		if len(segs) < 2 || !segs[1].called {
			return zodShape{}, false
		}
		rest = segs[2:]
		args := tsSplitTopLevel(segs[1].args)
		switch segs[1].name {
		case "object", "strictObject", "looseObject":
			s.kind = "object"
			s.keys, s.unread = f.entries(segs[1].args, depth)
		case "enum", "literal":
			s.kind = "enum"
			s.members = zodMembers(segs[1].args)
		case "union":
			s.kind = "union"
			s.members = zodMembers(segs[1].args)
		case "discriminatedUnion":
			s.kind = "union"
			if len(args) == 2 {
				s.members = zodMembers(args[1])
			} else {
				s.unread = "z.discriminatedUnion without its two arguments"
			}
		default:
			s.kind = "other"
			s.unread = "z." + segs[1].name + " is not an object, enum or union"
		}
	case !segs[0].called && depth < maxZodDepth:
		base, ok := f.shape(segs[0].name, depth+1)
		if ok {
			s = base
			break
		}
		// A name this file doesn't declare is a schema only if it is used as one.
		if len(segs) < 2 || !zodObjectMethods[segs[1].name] {
			return zodShape{}, false
		}
		s = zodShape{kind: "object", unread: segs[0].name + " is not declared in this file"}
	default:
		return zodShape{}, false
	}

	for _, seg := range rest {
		if s.unread != "" {
			break
		}
		if zodNeutralMethods[seg.name] {
			continue
		}
		if s.kind != "object" {
			s.unread = "." + seg.name + " on " + s.kind
			break
		}
		switch seg.name {
		case "extend", "safeExtend":
			keys, unread := f.entries(seg.args, depth)
			s.unread = unread
			s.keys = zodUpsert(s.keys, keys)
		case "merge":
			other, ok := f.shape(strings.TrimSpace(seg.args), depth+1)
			if !ok || other.kind != "object" || other.unread != "" {
				s.unread = ".merge(" + oneLineTS(seg.args) + ") can't be read"
				break
			}
			s.keys = zodUpsert(s.keys, other.keys)
		case "pick", "omit":
			mask, ok := zodMask(seg.args)
			if !ok {
				s.unread = "." + seg.name + " without a literal mask"
				break
			}
			keep := seg.name == "pick"
			s.keys = slices.DeleteFunc(slices.Clone(s.keys), func(k zodKey) bool { return mask[k.name] != keep })
		case "partial", "required":
			mask, ok := zodMask(seg.args)
			if strings.TrimSpace(seg.args) != "" && !ok {
				s.unread = "." + seg.name + " without a literal mask"
				break
			}
			s.keys = slices.Clone(s.keys)
			for i := range s.keys {
				if mask == nil || mask[s.keys[i].name] {
					s.keys[i].optional = seg.name == "partial"
					if s.keys[i].optional {
						s.keys[i].def = ""
					}
				}
			}
		default:
			s.unread = "." + seg.name
		}
	}
	return s, true
}

// zodObjectMethods are the methods that make a new object schema from one.
var zodObjectMethods = map[string]bool{
	"extend": true, "safeExtend": true, "merge": true, "pick": true, "omit": true, "partial": true, "required": true,
}

// zodNeutralMethods leave a schema's keys and members as they are.
var zodNeutralMethods = map[string]bool{
	"strict": true, "passthrough": true, "strip": true, "loose": true, "catchall": true,
	"refine": true, "superRefine": true, "check": true, "describe": true, "meta": true, "register": true,
	"readonly": true, "optional": true, "nullable": true, "nullish": true, "default": true, "prefault": true,
}

// zodValidators check a value without changing its type.
var zodValidators = map[string]bool{
	"min": true, "max": true, "length": true, "nonempty": true, "email": true, "url": true, "uuid": true,
	"regex": true, "trim": true, "toLowerCase": true, "toUpperCase": true, "int": true, "positive": true,
	"negative": true, "nonnegative": true, "nonpositive": true, "finite": true, "safe": true, "multipleOf": true,
	"gt": true, "gte": true, "lt": true, "lte": true, "startsWith": true, "endsWith": true, "includes": true,
	"datetime": true, "date": true, "time": true, "ip": true, "cuid": true, "describe": true, "meta": true,
	"refine": true, "superRefine": true, "check": true, "register": true,
}

// entries reads the keys of the object literal a call is given.
func (f *zodFile) entries(args string, depth int) ([]zodKey, string) {
	parts := tsSplitTopLevel(args)
	if len(parts) == 0 {
		return nil, "an object schema without its keys"
	}
	body, ok := tsBraced(parts[0], '{')
	if !ok {
		return nil, "keys that come from " + oneLineTS(parts[0])
	}
	var keys []zodKey
	for _, entry := range tsSplitTopLevel(body) {
		name, value, ok := tsProperty(entry)
		if !ok {
			return nil, "a key written as " + oneLineTS(entry)
		}
		key := f.key(value, depth)
		key.name = name
		keys = append(keys, key)
	}
	return keys, ""
}

// key reads one key's schema: what it is without validators, and whether the output
// type has to have it.
func (f *zodFile) key(expr string, depth int) zodKey {
	key := zodKey{core: zodSqueeze(expr), show: oneLineTS(expr)}
	segs, ok := zodChain(expr)
	if !ok || len(segs) == 0 {
		return key
	}
	methods := segs[1:]
	if segs[0].name == "z" && !segs[0].called {
		if len(segs) > 1 {
			key.optional = segs[1].name == "optional"
			methods = segs[2:]
		}
	} else if depth < maxZodDepth {
		// A schema kept under a name, or made by a helper: `id`, `text(100)`.
		if body, _, _, ok := tsDeclaredValue(f.lines, segs[0].name); ok {
			// A helper counts where it is called, a schema where it is named.
			arrow := tsArrowBody(body)
			if segs[0].called && arrow != "" {
				body = arrow
			}
			if segs[0].called == (arrow != "") {
				inner := f.key(body, depth+1)
				key.optional, key.def = inner.optional, inner.def
			}
		}
	}
	var core, show []string
	for i, seg := range segs {
		typed := true
		if i >= len(segs)-len(methods) {
			switch seg.name {
			case "optional", "exactOptional":
				key.optional, key.def, typed = true, "", false
			case "nullish":
				key.optional, key.def = true, ""
			case "default", "prefault", "catch":
				key.optional, typed = false, false
				key.def = oneLineTS(seg.args)
			default:
				typed = !zodValidators[seg.name]
			}
		}
		if typed {
			core = append(core, seg.text(zodSqueeze))
			show = append(show, seg.text(oneLineTS))
		}
	}
	key.core, key.show = strings.Join(core, "."), strings.Join(show, ".")
	return key
}

func zodUpsert(keys, more []zodKey) []zodKey {
	out := slices.Clone(keys)
	for _, k := range more {
		if i := slices.IndexFunc(out, func(o zodKey) bool { return o.name == k.name }); i >= 0 {
			out[i] = k
		} else {
			out = append(out, k)
		}
	}
	return out
}

// zodMask reads `{ a: true, b: true }`.
func zodMask(args string) (map[string]bool, bool) {
	body, ok := tsBraced(strings.TrimSpace(args), '{')
	if !ok {
		return nil, false
	}
	mask := make(map[string]bool)
	for _, entry := range tsSplitTopLevel(body) {
		name, value, ok := tsProperty(entry)
		if !ok {
			return nil, false
		}
		mask[name] = strings.TrimSpace(value) != "false"
	}
	return mask, true
}

// zodMembers returns the members of an enum or union: the entries of its array, or
// the argument itself where it is a name, so a changed name still shows.
func zodMembers(args string) []string {
	parts := tsSplitTopLevel(args)
	if len(parts) == 0 {
		return nil
	}
	body, ok := tsBraced(parts[0], '[')
	if !ok {
		return []string{oneLineTS(parts[0])}
	}
	var members []string
	for _, m := range tsSplitTopLevel(body) {
		members = append(members, oneLineTS(m))
	}
	return members
}

// zodSeg is one link of a chain: a name, and the arguments if it is called.
type zodSeg struct {
	name, args string
	called     bool
}

func (s zodSeg) text(args func(string) string) string {
	if !s.called {
		return s.name
	}
	return s.name + "(" + args(s.args) + ")"
}

var tsIdentRe = regexp.MustCompile(`^[A-Za-z_$][\w$]*`)

// zodChain splits `z.object({…}).refine(…)` into z, object({…}) and refine(…). False
// if the text is something other than names and calls joined by dots.
func zodChain(expr string) ([]zodSeg, bool) {
	expr = tsStripComments(expr)
	var segs []zodSeg
	i := 0
	skip := func() {
		for i < len(expr) && strings.IndexByte(" \t\r\n", expr[i]) >= 0 {
			i++
		}
	}
	for {
		skip()
		name := tsIdentRe.FindString(expr[i:])
		if name == "" {
			return nil, false
		}
		i += len(name)
		seg := zodSeg{name: name}
		skip()
		if i < len(expr) && expr[i] == '<' { // Type arguments of a call
			end := matchingBracket(expr[i:], '<', '>')
			if end < 0 {
				return nil, false
			}
			i += end + 1
			skip()
		}
		if i < len(expr) && expr[i] == '(' {
			end := tsMatching(expr, i)
			if end < 0 {
				return nil, false
			}
			seg.args, seg.called = expr[i+1:end], true
			i = end + 1
		}
		segs = append(segs, seg)
		skip()
		if i < len(expr) && expr[i] == '!' {
			i++
			skip()
		}
		if strings.HasPrefix(expr[i:], "?.") {
			i += 2
			continue
		}
		if i < len(expr) && expr[i] == '.' {
			i++
			continue
		}
		break
	}
	rest := strings.TrimSpace(expr[i:])
	if rest == "" || rest == ";" || strings.HasPrefix(rest, "as ") || strings.HasPrefix(rest, "satisfies ") {
		return segs, true
	}
	return nil, false
}

// tsDeclaredValue returns the initializer of the module-level variable a file declares
// under name, and the lines the statement spans.
func tsDeclaredValue(lines []string, name string) (expr string, first, last int, ok bool) {
	for i, text := range lines {
		m := tsValueDeclRe.FindStringSubmatch(text)
		if m == nil || m[1] != name {
			continue
		}
		first = i + 1
		shown := make(map[int]string, len(lines)-i)
		for l := first; l <= len(lines); l++ {
			shown[l] = lines[l-1]
		}
		last = newTSSource(shown).valueEnd(first)
		if last == 0 {
			return "", 0, 0, false
		}
		own := make(map[int]string, last-first+1)
		for l := first; l <= last; l++ {
			own[l] = lines[l-1]
		}
		expr = strings.TrimSpace(newTSSource(own).initializer(first))
		return strings.TrimSpace(strings.TrimSuffix(expr, ";")), first, last, expr != ""
	}
	return "", 0, 0, false
}

// tsArrowBody returns what an arrow function written as an expression returns:
// `z.string().max(max)` for `(max = 500) => z.string().max(max)`. Empty for anything
// else, a block body included.
func tsArrowBody(expr string) string {
	at := -1
	tsScan(expr, func(i, depth int) bool {
		if depth == 0 && strings.HasPrefix(expr[i:], "=>") {
			at = i + 2
			return false
		}
		return true
	})
	if at < 0 {
		return ""
	}
	body := strings.TrimSpace(expr[at:])
	if strings.HasPrefix(body, "{") {
		return ""
	}
	return body
}

// tsMatching returns the index of the bracket closing the one at s[open], or -1.
func tsMatching(s string, open int) int {
	end := -1
	rest := s[open:]
	tsScan(rest, func(i, depth int) bool {
		if depth == 1 && (rest[i] == ')' || rest[i] == ']' || rest[i] == '}') {
			end = open + i
			return false
		}
		return true
	})
	return end
}

// tsBraced returns what is inside a text that is one bracketed literal, `{…}` or `[…]`.
func tsBraced(s string, open byte) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s[0] != open {
		return "", false
	}
	end := tsMatching(s, 0)
	if end < 0 || strings.TrimSpace(s[end+1:]) != "" {
		return "", false
	}
	return s[1:end], true
}

// tsSplitTopLevel splits the entries of a literal or an argument list at its commas,
// without comments and without the empty entry a trailing comma leaves.
func tsSplitTopLevel(s string) []string {
	s = tsStripComments(s)
	var parts []string
	start := 0
	tsScan(s, func(i, depth int) bool {
		if depth == 0 && s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
		return true
	})
	parts = append(parts, s[start:])
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var tsObjectPropertyRe = regexp.MustCompile(`^(?:([A-Za-z_$][\w$]*)|"([^"]+)"|'([^']+)')\s*:`)

// tsProperty reads one entry of an object literal: `key: value`, `"key": value` or the
// shorthand `key`. A spread or a computed key is neither.
func tsProperty(entry string) (name, value string, ok bool) {
	entry = strings.TrimSpace(entry)
	if m := tsObjectPropertyRe.FindStringSubmatch(entry); m != nil {
		return m[1] + m[2] + m[3], strings.TrimSpace(entry[len(m[0]):]), true
	}
	if tsIdentRe.FindString(entry) == entry && entry != "" {
		return entry, entry, true
	}
	return "", "", false
}

// tsStripComments removes the comments from TypeScript source, leaving strings alone.
func tsStripComments(s string) string {
	if !strings.Contains(s, "//") && !strings.Contains(s, "/*") {
		return s
	}
	var b strings.Builder
	prev := -1
	keep := func(to int) {
		if to <= prev+1 {
			return
		}
		skipped := s[prev+1 : to]
		switch {
		case strings.HasPrefix(skipped, "//"):
		case strings.HasPrefix(skipped, "/*"):
			b.WriteByte(' ')
		default:
			b.WriteString(skipped)
		}
	}
	tsScan(s, func(i, depth int) bool {
		keep(i)
		b.WriteByte(s[i])
		prev = i
		return true
	})
	keep(len(s))
	return b.String()
}

// oneLineTS folds source over several lines into one, without the space and the
// trailing comma that only the line breaks put there.
func oneLineTS(s string) string {
	s = strings.Join(strings.Fields(tsStripComments(s)), " ")
	for _, pair := range [][2]string{{"( ", "("}, {" )", ")"}, {"[ ", "["}, {" ]", "]"}, {",)", ")"}, {",]", "]"}, {", }", " }"}, {",}", "}"}} {
		s = strings.ReplaceAll(s, pair[0], pair[1])
	}
	return s
}

// zodSqueeze is oneLineTS without any space: two spellings of one schema compare equal.
func zodSqueeze(s string) string {
	return strings.ReplaceAll(oneLineTS(s), " ", "")
}
