package app

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func mustDoc(t *testing.T, s string) Doc {
	t.Helper()
	d, err := parseDoc(json.RawMessage(s), "doc")
	if err != nil {
		t.Fatalf("parseDoc(%s): %v", s, err)
	}
	return d
}

func docJSON(t *testing.T, d Doc) string {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func jsonEq(t *testing.T, got interface{}, want string) {
	t.Helper()
	gb, _ := json.Marshal(got)
	var g, w interface{}
	_ = json.Unmarshal(gb, &g)
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad want JSON: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("got  %s\nwant %s", gb, want)
	}
}

// ---- property tests ----

// randEntity builds a random entity. Values are a function of (name, clock)
// and immutable fields a function of the key: real clients never emit two
// values with the same HLC string, nor disagree on an immutable field — the
// preconditions under which LWW merge is a CRDT.
func randEntity(r *rand.Rand) *Entity {
	e := &Entity{F: map[string]json.RawMessage{}, R: map[string]Register{}}
	for _, k := range []string{"id", "bookId", "created"} {
		if r.Intn(2) == 0 {
			e.F[k] = json.RawMessage(fmt.Sprintf("%q", "f-"+k))
		}
	}
	for _, name := range []string{"name", "note", "$money", "deleted"} {
		if r.Intn(2) == 0 {
			t := fmt.Sprintf("%02d", r.Intn(6))
			e.R[name] = Register{V: json.RawMessage(fmt.Sprintf("%q", name+"@"+t)), T: t}
		}
	}
	if len(e.F) == 0 && len(e.R) == 0 {
		e.R["name"] = Register{V: json.RawMessage(`"name@00"`), T: "00"}
	}
	return e
}

func randDoc(r *rand.Rand) Doc {
	d := newDoc()
	if r.Intn(2) == 0 {
		d.Book = randEntity(r)
	}
	for i := 0; i < 4; i++ {
		if r.Intn(2) == 0 {
			d.Members[fmt.Sprintf("m%d", i)] = randEntity(r)
		}
		if r.Intn(2) == 0 {
			d.Records[fmt.Sprintf("r%d", i)] = randEntity(r)
		}
	}
	return d
}

// m merges with version 0 so _v never distinguishes results.
func m(a, b Doc) Doc {
	out, _ := mergeDoc(a, b, 0)
	return out
}

func TestMergeProperties(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	empty := newDoc()
	for i := 0; i < 2000; i++ {
		a, b, c := randDoc(r), randDoc(r), randDoc(r)
		aJSON, bJSON := docJSON(t, a), docJSON(t, b)

		// Commutative.
		if x, y := docJSON(t, m(m(empty, a), b)), docJSON(t, m(m(empty, b), a)); x != y {
			t.Fatalf("not commutative:\n a=%s\n b=%s\n ab=%s\n ba=%s", aJSON, bJSON, x, y)
		}
		// Associative (in both groupings, starting from an arbitrary state).
		if x, y := docJSON(t, m(m(a, b), c)), docJSON(t, m(a, m(b, c))); x != y {
			t.Fatalf("not associative:\n (ab)c=%s\n a(bc)=%s", x, y)
		}
		// Order of delivering three change sets does not matter.
		if x, y := docJSON(t, m(m(m(empty, a), b), c)), docJSON(t, m(m(m(empty, c), a), b)); x != y {
			t.Fatalf("delivery order matters:\n %s\n %s", x, y)
		}
		// Idempotent: merging again changes nothing and reports no change.
		ab := m(a, b)
		again, changed := mergeDoc(ab, b, 0)
		if changed || docJSON(t, again) != docJSON(t, ab) {
			t.Fatalf("not idempotent (changed=%v)", changed)
		}
		if _, changed := mergeDoc(a, a, 0); changed {
			t.Fatalf("merge(a, a) reported a change: %s", aJSON)
		}
		// Pure: inputs are not mutated.
		if docJSON(t, a) != aJSON || docJSON(t, b) != bJSON {
			t.Fatal("mergeDoc mutated an input")
		}
	}
}

// ---- merge rules ----

func TestMergeRegisterStrictlyGreaterWins(t *testing.T) {
	stored := mustDoc(t, `{"records":{"r1":{"r":{"note":{"v":"old","t":"B"}}}}}`)

	out, changed := mergeDoc(stored, mustDoc(t, `{"records":{"r1":{"r":{"note":{"v":"tie","t":"B"}}}}}`), 2)
	if changed || string(out.Records["r1"].R["note"].V) != `"old"` {
		t.Errorf("equal clock must keep stored: changed=%v note=%s", changed, out.Records["r1"].R["note"].V)
	}
	out, changed = mergeDoc(stored, mustDoc(t, `{"records":{"r1":{"r":{"note":{"v":"older","t":"A"}}}}}`), 2)
	if changed || string(out.Records["r1"].R["note"].V) != `"old"` {
		t.Errorf("older clock must lose: changed=%v", changed)
	}
	out, changed = mergeDoc(stored, mustDoc(t, `{"records":{"r1":{"r":{"note":{"v":"new","t":"C"}}}}}`), 2)
	if !changed || string(out.Records["r1"].R["note"].V) != `"new"` || out.Records["r1"].V != 2 {
		t.Errorf("newer clock must win and stamp _v: changed=%v %s", changed, docJSON(t, out))
	}
	// Byte-wise comparison, not numeric: "10" < "9".
	out, _ = mergeDoc(mustDoc(t, `{"book":{"r":{"name":{"v":"a","t":"9"}}}}`), mustDoc(t, `{"book":{"r":{"name":{"v":"b","t":"10"}}}}`), 1)
	if string(out.Book.R["name"].V) != `"a"` {
		t.Errorf("clock comparison must be byte-wise")
	}
	if string(stored.Records["r1"].R["note"].V) != `"old"` || stored.Records["r1"].V != 0 {
		t.Error("stored doc was mutated")
	}
}

func TestMergeImmutableFirstWriterWins(t *testing.T) {
	stored := mustDoc(t, `{"records":{"r1":{"f":{"id":"r1","bookId":"b1"}}}}`)
	out, changed := mergeDoc(stored, mustDoc(t, `{"records":{"r1":{"f":{"bookId":"OTHER"}}}}`), 3)
	if changed || string(out.Records["r1"].F["bookId"]) != `"b1"` {
		t.Errorf("f key must never be overwritten: changed=%v", changed)
	}
	out, changed = mergeDoc(stored, mustDoc(t, `{"records":{"r1":{"f":{"bookId":"OTHER","created":"c1"}}}}`), 3)
	if !changed || string(out.Records["r1"].F["created"]) != `"c1"` || string(out.Records["r1"].F["bookId"]) != `"b1"` {
		t.Errorf("absent f key must be added (only): changed=%v %s", changed, docJSON(t, out))
	}
}

func TestMergeChangedDetectionAndVersionStamp(t *testing.T) {
	stored := mustDoc(t, `{"book":{"r":{"name":{"v":"Trip","t":"1"}}},
		"members":{"m1":{"f":{"created":"c"},"r":{"name":{"v":"A","t":"1"}}}},
		"records":{"r1":{"r":{"note":{"v":"x","t":"1"}}},"r2":{"r":{"note":{"v":"y","t":"1"}}}}}`)
	stampAll(stored, 5)

	changes := mustDoc(t, `{"book":{"r":{"name":{"v":"Trip","t":"1"}}},
		"members":{"m1":{"r":{"name":{"v":"A","t":"0"}}},"m2":{"f":{"created":"d"}}},
		"records":{"r1":{"r":{"note":{"v":"x2","t":"2"}}}}}`)
	out, changed := mergeDoc(stored, changes, 6)
	if !changed {
		t.Fatal("expected a change")
	}
	want := map[string]int64{"book": 5, "m1": 5, "m2": 6, "r1": 6, "r2": 5}
	got := map[string]int64{"book": out.Book.V, "m1": out.Members["m1"].V, "m2": out.Members["m2"].V, "r1": out.Records["r1"].V, "r2": out.Records["r2"].V}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("_v = %v, want %v", got, want)
	}

	// An entity with nothing in it adds nothing.
	if _, changed := mergeDoc(stored, mustDoc(t, `{"records":{"r9":{}}}`), 6); changed {
		t.Error("empty entity must not count as a change")
	}
}

func TestParseDocIgnoresUnderscoreKeys(t *testing.T) {
	d := mustDoc(t, `{"_meta":1,"book":{"_v":99,"_x":{},"r":{"name":{"v":"B","t":"1"}}},
		"records":{"r1":{"_v":42,"f":{"id":"r1"},"r":{"note":{"v":null,"t":"1"}}}}}`)
	if d.Book.V != 0 || d.Records["r1"].V != 0 {
		t.Errorf("incoming _v must be ignored: book=%d r1=%d", d.Book.V, d.Records["r1"].V)
	}
	jsonEq(t, d, `{"book":{"f":{},"r":{"name":{"v":"B","t":"1"}},"_v":0},"members":{},
		"records":{"r1":{"f":{"id":"r1"},"r":{"note":{"v":null,"t":"1"}},"_v":0}}}`)
}

func TestParseDocValidation(t *testing.T) {
	long := strings.Repeat("x", 65)
	bad := map[string]string{
		"null doc":        `null`,
		"array doc":       `[]`,
		"members array":   `{"members":[]}`,
		"entity string":   `{"records":{"r1":"x"}}`,
		"empty id":        `{"records":{"":{}}}`,
		"long id":         `{"records":{"` + long + `":{}}}`,
		"f not object":    `{"book":{"f":[]}}`,
		"r not object":    `{"book":{"r":1}}`,
		"register scalar": `{"book":{"r":{"name":"x"}}}`,
		"missing v":       `{"book":{"r":{"name":{"t":"1"}}}}`,
		"missing t":       `{"book":{"r":{"name":{"v":1}}}}`,
		"empty t":         `{"book":{"r":{"name":{"v":1,"t":""}}}}`,
		"numeric t":       `{"book":{"r":{"name":{"v":1,"t":1}}}}`,
		"long t":          `{"book":{"r":{"name":{"v":1,"t":"` + long + `"}}}}`,
	}
	for name, s := range bad {
		if _, err := parseDoc(json.RawMessage(s), "doc"); err == nil {
			t.Errorf("%s: expected error for %s", name, s)
		}
	}
	ok := []string{`{}`, `{"book":null,"members":null}`, `{"book":{}}`,
		`{"records":{"` + strings.Repeat("x", 64) + `":{"r":{"a":{"v":1,"t":"` + strings.Repeat("9", 64) + `"}}}}}`}
	for _, s := range ok {
		if _, err := parseDoc(json.RawMessage(s), "doc"); err != nil {
			t.Errorf("unexpected error for %s: %v", s, err)
		}
	}

	var sb strings.Builder
	sb.WriteString(`{"members":{`)
	for i := 0; i <= maxDocMembers; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		fmt.Fprintf(&sb, `"m%d":{}`, i)
	}
	sb.WriteString(`}}`)
	if _, err := parseDoc(json.RawMessage(sb.String()), "doc"); err == nil {
		t.Errorf("expected error for %d members", maxDocMembers+1)
	}
}

// ---- since filtering ----

func TestFilterSince(t *testing.T) {
	d := newDoc()
	d.Book = &Entity{F: map[string]json.RawMessage{}, R: map[string]Register{}, V: 2}
	d.Members["m1"] = &Entity{F: map[string]json.RawMessage{}, R: map[string]Register{}, V: 1}
	d.Records["r1"] = &Entity{F: map[string]json.RawMessage{}, R: map[string]Register{}, V: 3}
	d.Records["r2"] = &Entity{F: map[string]json.RawMessage{}, R: map[string]Register{}, V: 2}

	all := filterSince(d, 0)
	if all.Book == nil || len(all.Members) != 1 || len(all.Records) != 2 {
		t.Errorf("since=0 must return everything: %s", docJSON(t, all))
	}
	f := filterSince(d, 2)
	if f.Book != nil || len(f.Members) != 0 || len(f.Records) != 1 || f.Records["r1"] == nil {
		t.Errorf("since=2: %s", docJSON(t, f))
	}
	if got := docJSON(t, filterSince(d, 3)); got != `{"members":{},"records":{}}` {
		t.Errorf("since=3 = %s, want no book key and empty maps", got)
	}
}

// ---- flatten ----

func TestFlattenRecordExample(t *testing.T) {
	d := mustDoc(t, `{"records":{"r1":{"f":{"id":"r1","bookId":"b1"},"r":{
		"$money":{"v":{"type":"expense","amount":100,"paidById":"m1","splitAmongIds":["m1"]},"t":"x"},
		"note":{"v":"lunch","t":"y"},"deleted":{"v":false,"t":"z"}}}}}`)
	p := flatten(d)
	jsonEq(t, p["records"], `[{"id":"r1","bookId":"b1","type":"expense","amount":100,"paidById":"m1","splitAmongIds":["m1"],"note":"lunch"}]`)
	jsonEq(t, p["deletedIds"], `[]`)
}

func TestFlattenNumbersVerbatim(t *testing.T) {
	d := mustDoc(t, `{"records":{"r1":{"r":{"amount":{"v":12345678901234567890,"t":"1"}}}}}`)
	b, _ := json.Marshal(flatten(d)["records"])
	if string(b) != `[{"amount":12345678901234567890}]` {
		t.Errorf("number not preserved: %s", b)
	}
}

func TestFlattenSpreadAndDeleted(t *testing.T) {
	d := mustDoc(t, `{"records":{
		"r3":{"f":{"id":"r3"},"r":{"$a":{"v":"scalar","t":"1"},"$b":{"v":{"x":1,"y":2},"t":"1"},"$c":{"v":{"y":3},"t":"1"}}},
		"r2":{"f":{"id":"r2"},"r":{"note":{"v":"gone","t":"1"},"deleted":{"v":true,"t":"1"}}},
		"r1":{"f":{"id":"r1"},"r":{"deleted":{"v":null,"t":"1"}}}
	}}`)
	p := flatten(d)
	// $a with a non-object value is kept under its own name; $b then $c spread
	// in name order; r2 is excluded and listed; `deleted` never appears.
	jsonEq(t, p["records"], `[{"id":"r1"},{"id":"r3","$a":"scalar","x":1,"y":3}]`)
	jsonEq(t, p["deletedIds"], `["r2"]`)
}

func TestFlattenBookAndMembers(t *testing.T) {
	d := mustDoc(t, `{
		"book":{"f":{"id":"b1","createdAt":"2026-01-01"},"r":{"name":{"v":"Trip","t":"1"},"$cfg":{"v":{"currency":"JPY"},"t":"1"}}},
		"members":{
			"mB":{"f":{"id":"mB","created":"2"},"r":{"name":{"v":"B","t":"1"}}},
			"mA":{"f":{"id":"mA","created":"2"},"r":{"name":{"v":"A","t":"1"},"archived":{"v":true,"t":"1"}}},
			"mZ":{"f":{"id":"mZ","created":"1"},"r":{"name":{"v":"Z","t":"1"}}}
		}}`)
	p := flatten(d)
	jsonEq(t, p["book"], `{"id":"b1","createdAt":"2026-01-01","name":"Trip","currency":"JPY","members":[
		{"id":"mZ","created":"1","name":"Z"},
		{"id":"mA","created":"2","name":"A","archived":true},
		{"id":"mB","created":"2","name":"B"}]}`)
	jsonEq(t, p["records"], `[]`)

	// No book entity at all still yields a book object with members.
	jsonEq(t, flatten(newDoc()), `{"book":{"members":[]},"records":[],"deletedIds":[]}`)
}

func TestUpgradeBaseMustMatchCurrentPayload(t *testing.T) {
	p1 := []byte(`{"book": {"id": "b1"}, "records": []}`)
	p2 := []byte(`{"book": {"id": "b1"}, "records": [{"id": "r9"}]}`) // a v1 PUT landed
	base := newDoc()

	if legacyHash(p1) != legacyHash(append([]byte{}, p1...)) {
		t.Fatal("legacyHash must be stable for the same payload")
	}
	if !baseMatchesPayload(&base, legacyHash(p1), p1) {
		t.Fatal("a base converted from the current payload must be accepted")
	}
	if baseMatchesPayload(&base, legacyHash(p1), p2) {
		t.Fatal("a base converted from an older payload must be rejected")
	}
	if baseMatchesPayload(&base, "", p1) {
		t.Fatal("a base without baseOf must be rejected")
	}
	if baseMatchesPayload(nil, legacyHash(p1), p1) {
		t.Fatal("no base, no upgrade")
	}
}
