package app

import (
	"encoding/json"
	"reflect"
	"testing"
)

// payload decodes a JSON literal the way ShouldBindJSON / json.Unmarshal would,
// so the tests exercise the same []interface{} / map[string]interface{} shapes.
func payload(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad test JSON: %v", err)
	}
	return m
}

func bookOf(t *testing.T, merged map[string]interface{}) map[string]interface{} {
	t.Helper()
	b, ok := merged["book"].(map[string]interface{})
	if !ok {
		t.Fatalf("merged book missing or not an object: %#v", merged["book"])
	}
	return b
}

func idsOf(t *testing.T, v interface{}) []string {
	t.Helper()
	items, ok := v.([]interface{})
	if !ok {
		t.Fatalf("expected array, got %#v", v)
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.(map[string]interface{})["id"].(string))
	}
	return ids
}

func TestMergeBookPreservesFieldMissingFromIncoming(t *testing.T) {
	existing := payload(t, `{"book":{"id":"b1","name":"Trip","currency":"JPY","members":[]},"records":[]}`)
	incoming := payload(t, `{"book":{"id":"b1","name":"Trip 2026","members":[]},"records":[]}`)

	book := bookOf(t, mergeSharedPayload(existing, incoming))
	if book["currency"] != "JPY" {
		t.Errorf("currency = %#v, want JPY (older client must not erase it)", book["currency"])
	}
	if book["name"] != "Trip 2026" {
		t.Errorf("name = %#v, want incoming value", book["name"])
	}
}

func TestMergeBookIncomingValueWins(t *testing.T) {
	existing := payload(t, `{"book":{"id":"b1","name":"Trip","currency":"JPY"}}`)
	incoming := payload(t, `{"book":{"id":"b1","name":"Trip","currency":"TWD"}}`)

	book := bookOf(t, mergeSharedPayload(existing, incoming))
	if book["currency"] != "TWD" {
		t.Errorf("currency = %#v, want TWD", book["currency"])
	}
}

func TestMergeBookWithoutIncomingBookKeepsExisting(t *testing.T) {
	existing := payload(t, `{"book":{"id":"b1","currency":"JPY","members":[{"id":"m1"}]}}`)
	incoming := payload(t, `{"records":[]}`)

	book := bookOf(t, mergeSharedPayload(existing, incoming))
	if book["currency"] != "JPY" {
		t.Errorf("currency = %#v, want JPY", book["currency"])
	}
	if got := idsOf(t, book["members"]); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Errorf("members = %v, want [m1]", got)
	}
}

func TestMergeMembersUnion(t *testing.T) {
	existing := payload(t, `{"book":{"id":"b1","members":[{"id":"m1","name":"A"},{"id":"m2","name":"B"}]}}`)
	incoming := payload(t, `{"book":{"id":"b1","members":[{"id":"m2","name":"B renamed"},{"id":"m3","name":"C"}]}}`)

	book := bookOf(t, mergeSharedPayload(existing, incoming))
	if got := idsOf(t, book["members"]); !reflect.DeepEqual(got, []string{"m1", "m2", "m3"}) {
		t.Fatalf("members = %v, want [m1 m2 m3]", got)
	}
	m2 := book["members"].([]interface{})[1].(map[string]interface{})
	if m2["name"] != "B renamed" {
		t.Errorf("m2 name = %#v, want incoming value", m2["name"])
	}
}

func TestMergeDeletedMemberIds(t *testing.T) {
	existing := payload(t, `{"book":{"id":"b1","members":[{"id":"m1"},{"id":"m2"}]}}`)
	incoming := payload(t, `{"book":{"id":"b1","members":[{"id":"m1"}]},"deletedMemberIds":["m2"]}`)

	book := bookOf(t, mergeSharedPayload(existing, incoming))
	if got := idsOf(t, book["members"]); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Errorf("members = %v, want [m1]", got)
	}
}

func TestMergeMemberNotDroppedWithoutDeletedMemberIds(t *testing.T) {
	existing := payload(t, `{"book":{"id":"b1","members":[{"id":"m1"},{"id":"m2"}]}}`)
	incoming := payload(t, `{"book":{"id":"b1","members":[{"id":"m1"}]}}`)

	book := bookOf(t, mergeSharedPayload(existing, incoming))
	if got := idsOf(t, book["members"]); !reflect.DeepEqual(got, []string{"m1", "m2"}) {
		t.Errorf("members = %v, want [m1 m2]", got)
	}
}

func TestMergeDeletedIds(t *testing.T) {
	existing := payload(t, `{"records":[{"id":"r1"},{"id":"r2"},{"id":"r3"}]}`)
	incoming := payload(t, `{"records":[{"id":"r3"}],"deletedIds":["r2","r3"]}`)

	merged := mergeSharedPayload(existing, incoming)
	if got := idsOf(t, merged["records"]); !reflect.DeepEqual(got, []string{"r1"}) {
		t.Errorf("records = %v, want [r1]", got)
	}
}

func TestMergeRecordsIncomingWinsByID(t *testing.T) {
	existing := payload(t, `{"records":[{"id":"r1","amount":10,"amountCurrency":"JPY","note":"old"},{"id":"r2","amount":5}]}`)
	incoming := payload(t, `{"records":[{"id":"r1","amount":20},{"id":"r4","amount":1}]}`)

	merged := mergeSharedPayload(existing, incoming)
	if got := idsOf(t, merged["records"]); !reflect.DeepEqual(got, []string{"r1", "r2", "r4"}) {
		t.Fatalf("records = %v, want [r1 r2 r4]", got)
	}
	r1 := merged["records"].([]interface{})[0].(map[string]interface{})
	want := map[string]interface{}{"id": "r1", "amount": float64(20)}
	if !reflect.DeepEqual(r1, want) {
		t.Errorf("r1 = %#v, want whole incoming record %#v", r1, want)
	}
}

func TestMergeNilExisting(t *testing.T) {
	incoming := payload(t, `{"book":{"id":"b1","currency":"TWD","members":[{"id":"m1"}]},"records":[{"id":"r1"}],"deletedIds":["zz"]}`)

	merged := mergeSharedPayload(nil, incoming)
	book := bookOf(t, merged)
	if book["currency"] != "TWD" {
		t.Errorf("currency = %#v, want TWD", book["currency"])
	}
	if got := idsOf(t, book["members"]); !reflect.DeepEqual(got, []string{"m1"}) {
		t.Errorf("members = %v, want [m1]", got)
	}
	if got := idsOf(t, merged["records"]); !reflect.DeepEqual(got, []string{"r1"}) {
		t.Errorf("records = %v, want [r1]", got)
	}
	if _, ok := merged["deletedIds"]; ok {
		t.Error("deletedIds must not be stored in the merged payload")
	}
}

func TestMergeDoesNotMutateInputs(t *testing.T) {
	const existingJSON = `{"book":{"id":"b1","currency":"JPY","members":[{"id":"m1"}]},"records":[{"id":"r1"}]}`
	const incomingJSON = `{"book":{"id":"b1","name":"N","members":[{"id":"m2"}]},"records":[{"id":"r2"}],"deletedIds":["r1"],"deletedMemberIds":["m1"]}`
	existing := payload(t, existingJSON)
	incoming := payload(t, incomingJSON)

	_ = mergeSharedPayload(existing, incoming)

	if !reflect.DeepEqual(existing, payload(t, existingJSON)) {
		t.Errorf("existing was mutated: %#v", existing)
	}
	if !reflect.DeepEqual(incoming, payload(t, incomingJSON)) {
		t.Errorf("incoming was mutated: %#v", incoming)
	}
}
