package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Shared-book sync v2: a state-based CRDT. A Doc is a set of entities (the book,
// members and records by id); each entity is immutable fields `f` (first writer
// wins) plus last-writer-wins registers `r` keyed by a hybrid-logical-clock
// string `t` (plain byte-wise comparison, strictly greater wins, ties keep the
// stored value). The server never looks inside `v` or `f` values except for the
// flatten conventions below — the frontend owns the schema.
//
// Merge is commutative, associative and idempotent provided clients never emit
// two different values with the same clock (HLC strings carry a node id) and
// never disagree on an immutable field.

const (
	maxClockLen   = 64
	maxEntityID   = 64
	maxDocMembers = 5000
	maxDocRecords = 20000
	maxV2Body     = 4 << 20
)

// Register is one last-writer-wins value.
type Register struct {
	V json.RawMessage `json:"v"`
	T string          `json:"t"`
}

// Entity is `{"f": {...}, "r": {...}, "_v": N}`. V (`_v`) is server-owned: the
// space version at which the entity last changed; it is ignored on input.
type Entity struct {
	F map[string]json.RawMessage `json:"f"`
	R map[string]Register        `json:"r"`
	V int64                      `json:"_v"`
}

// Doc is a full or partial CRDT document. Book is omitted when nil; Members and
// Records are always encoded as objects (callers keep them non-nil via newDoc /
// normalize).
type Doc struct {
	Book    *Entity            `json:"book,omitempty"`
	Members map[string]*Entity `json:"members"`
	Records map[string]*Entity `json:"records"`
}

func newDoc() Doc {
	return Doc{Members: map[string]*Entity{}, Records: map[string]*Entity{}}
}

// normalize fills nil maps (e.g. after decoding a stored doc).
func (d *Doc) normalize() {
	if d.Members == nil {
		d.Members = map[string]*Entity{}
	}
	if d.Records == nil {
		d.Records = map[string]*Entity{}
	}
	for _, e := range d.entities() {
		if e.F == nil {
			e.F = map[string]json.RawMessage{}
		}
		if e.R == nil {
			e.R = map[string]Register{}
		}
	}
}

func (d *Doc) entities() []*Entity {
	out := make([]*Entity, 0, len(d.Members)+len(d.Records)+1)
	if d.Book != nil {
		out = append(out, d.Book)
	}
	for _, e := range d.Members {
		out = append(out, e)
	}
	for _, e := range d.Records {
		out = append(out, e)
	}
	return out
}

// ---- parsing / validation of client input ----

func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// parseObject decodes a JSON object; null / non-objects are an error.
func parseObject(raw json.RawMessage, where string) (map[string]json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 || isJSONNull(raw) {
		return nil, fmt.Errorf("%s must be a JSON object", where)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s must be a JSON object", where)
	}
	return m, nil
}

// parseDoc validates and decodes a client-sent Doc. Keys starting with `_`
// (including `_v`) are ignored at every entity/doc level, as are unknown keys.
func parseDoc(raw json.RawMessage, where string) (Doc, error) {
	doc := newDoc()
	top, err := parseObject(raw, where)
	if err != nil {
		return doc, err
	}
	if b, ok := top["book"]; ok && !isJSONNull(b) {
		e, err := parseEntity(b, where+".book")
		if err != nil {
			return doc, err
		}
		doc.Book = e
	}
	if doc.Members, err = parseEntityMap(top["members"], where+".members", maxDocMembers); err != nil {
		return doc, err
	}
	if doc.Records, err = parseEntityMap(top["records"], where+".records", maxDocRecords); err != nil {
		return doc, err
	}
	return doc, nil
}

func parseEntityMap(raw json.RawMessage, where string, limit int) (map[string]*Entity, error) {
	out := map[string]*Entity{}
	if raw == nil || isJSONNull(raw) {
		return out, nil
	}
	m, err := parseObject(raw, where)
	if err != nil {
		return nil, err
	}
	if len(m) > limit {
		return nil, fmt.Errorf("%s: at most %d entities allowed", where, limit)
	}
	for id, er := range m {
		if id == "" || len(id) > maxEntityID {
			return nil, fmt.Errorf("%s: entity id must be 1-%d bytes", where, maxEntityID)
		}
		e, err := parseEntity(er, where+"["+id+"]")
		if err != nil {
			return nil, err
		}
		out[id] = e
	}
	return out, nil
}

func parseEntity(raw json.RawMessage, where string) (*Entity, error) {
	m, err := parseObject(raw, where)
	if err != nil {
		return nil, err
	}
	e := &Entity{F: map[string]json.RawMessage{}, R: map[string]Register{}}
	if f, ok := m["f"]; ok && !isJSONNull(f) {
		fm, err := parseObject(f, where+".f")
		if err != nil {
			return nil, err
		}
		for k, v := range fm {
			e.F[k] = v
		}
	}
	if r, ok := m["r"]; ok && !isJSONNull(r) {
		rm, err := parseObject(r, where+".r")
		if err != nil {
			return nil, err
		}
		for name, regRaw := range rm {
			reg, err := parseRegister(regRaw, where+".r."+name)
			if err != nil {
				return nil, err
			}
			e.R[name] = reg
		}
	}
	return e, nil
}

func parseRegister(raw json.RawMessage, where string) (Register, error) {
	m, err := parseObject(raw, where)
	if err != nil {
		return Register{}, err
	}
	v, okV := m["v"]
	tRaw, okT := m["t"]
	if !okV || !okT {
		return Register{}, fmt.Errorf("%s must have both v and t", where)
	}
	var t string
	if err := json.Unmarshal(tRaw, &t); err != nil {
		return Register{}, fmt.Errorf("%s.t must be a string", where)
	}
	if t == "" || len(t) > maxClockLen {
		return Register{}, fmt.Errorf("%s.t must be 1-%d bytes", where, maxClockLen)
	}
	return Register{V: v, T: t}, nil
}

func checkDocLimits(d Doc) error {
	if len(d.Members) > maxDocMembers {
		return fmt.Errorf("doc would exceed %d members", maxDocMembers)
	}
	if len(d.Records) > maxDocRecords {
		return fmt.Errorf("doc would exceed %d records", maxDocRecords)
	}
	return nil
}

// ---- merge ----

// mergeEntity merges incoming into stored without mutating either. It returns
// the resulting entity and whether anything was taken from incoming; when
// nothing changed the stored pointer itself is returned. A new entity carries
// stored's _v; the caller stamps changed entities with the new version.
func mergeEntity(stored, incoming *Entity) (*Entity, bool) {
	if incoming == nil {
		return stored, false
	}
	var out *Entity
	ensure := func() {
		if out != nil {
			return
		}
		out = &Entity{F: map[string]json.RawMessage{}, R: map[string]Register{}}
		if stored != nil {
			for k, v := range stored.F {
				out.F[k] = v
			}
			for k, v := range stored.R {
				out.R[k] = v
			}
			out.V = stored.V
		}
	}
	for k, v := range incoming.F {
		if stored != nil {
			if _, ok := stored.F[k]; ok {
				continue // immutable: first writer wins
			}
		}
		ensure()
		out.F[k] = v
	}
	for name, in := range incoming.R {
		if stored != nil {
			if s, ok := stored.R[name]; ok && !(in.T > s.T) {
				continue // strictly greater clock wins; ties keep stored
			}
		}
		ensure()
		out.R[name] = in
	}
	if out == nil {
		return stored, false
	}
	return out, true
}

func mergeEntityMap(stored, incoming map[string]*Entity, newVersion int64) (map[string]*Entity, bool) {
	out := make(map[string]*Entity, len(stored)+len(incoming))
	for id, e := range stored {
		out[id] = e
	}
	changed := false
	for id, in := range incoming {
		if e, ch := mergeEntity(out[id], in); ch {
			e.V = newVersion
			out[id] = e
			changed = true
		}
	}
	return out, changed
}

// mergeDoc merges changes into stored (neither is mutated) and stamps every
// changed entity with _v = newVersion. It reports whether anything changed.
func mergeDoc(stored, changes Doc, newVersion int64) (Doc, bool) {
	out := Doc{Book: stored.Book}
	changed := false
	if e, ch := mergeEntity(stored.Book, changes.Book); ch {
		e.V = newVersion
		out.Book = e
		changed = true
	}
	var ch bool
	out.Members, ch = mergeEntityMap(stored.Members, changes.Members, newVersion)
	changed = changed || ch
	out.Records, ch = mergeEntityMap(stored.Records, changes.Records, newVersion)
	changed = changed || ch
	return out, changed
}

// stampAll sets _v on every entity of a freshly parsed doc (create / base).
func stampAll(d Doc, v int64) {
	for _, e := range d.entities() {
		e.V = v
	}
}

// filterSince returns the entities with _v > since. Book is omitted unless it
// changed; members/records are always (possibly empty) maps.
func filterSince(d Doc, since int64) Doc {
	out := newDoc()
	if d.Book != nil && d.Book.V > since {
		out.Book = d.Book
	}
	for id, e := range d.Members {
		if e.V > since {
			out.Members[id] = e
		}
	}
	for id, e := range d.Records {
		if e.V > since {
			out.Records[id] = e
		}
	}
	return out
}

// ---- flatten to a v1 payload ----

func decodeValue(raw json.RawMessage) interface{} {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep numbers verbatim
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	return v
}

// flattenEntity renders one entity as a plain v1 object: a copy of f, then every
// register (sorted by name for determinism) — `$name` registers whose value is
// an object are spread into the output, others become output[name] = v.
func flattenEntity(e *Entity) map[string]interface{} {
	out := map[string]interface{}{}
	if e == nil {
		return out
	}
	for k, v := range e.F {
		out[k] = decodeValue(v)
	}
	names := make([]string, 0, len(e.R))
	for name := range e.R {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := decodeValue(e.R[name].V)
		if strings.HasPrefix(name, "$") {
			if obj, ok := v.(map[string]interface{}); ok {
				for k, x := range obj {
					out[k] = x
				}
				continue
			}
		}
		out[name] = v
	}
	return out
}

func fString(e *Entity, key string) string {
	var s string
	if raw, ok := e.F[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// flatten renders a Doc as the v1 payload {book: {..., members}, records,
// deletedIds} so v1 clients can still read an upgraded space. Members are
// sorted by f.created then id (archived ones included); records whose
// flattened `deleted` is true are left out and listed in deletedIds; records
// are sorted by id and never carry the `deleted` key.
func flatten(d Doc) map[string]interface{} {
	book := flattenEntity(d.Book)

	memberIDs := make([]string, 0, len(d.Members))
	for id := range d.Members {
		memberIDs = append(memberIDs, id)
	}
	sort.Slice(memberIDs, func(i, j int) bool {
		ci, cj := fString(d.Members[memberIDs[i]], "created"), fString(d.Members[memberIDs[j]], "created")
		if ci != cj {
			return ci < cj
		}
		return memberIDs[i] < memberIDs[j]
	})
	members := make([]interface{}, 0, len(memberIDs))
	for _, id := range memberIDs {
		members = append(members, flattenEntity(d.Members[id]))
	}
	book["members"] = members

	recordIDs := make([]string, 0, len(d.Records))
	for id := range d.Records {
		recordIDs = append(recordIDs, id)
	}
	sort.Strings(recordIDs)
	records := make([]interface{}, 0, len(recordIDs))
	deletedIDs := make([]interface{}, 0)
	for _, id := range recordIDs {
		r := flattenEntity(d.Records[id])
		if del, _ := r["deleted"].(bool); del {
			deletedIDs = append(deletedIDs, id)
			continue
		}
		delete(r, "deleted")
		records = append(records, r)
	}

	return map[string]interface{}{
		"book":       book,
		"records":    records,
		"deletedIds": deletedIDs,
	}
}
