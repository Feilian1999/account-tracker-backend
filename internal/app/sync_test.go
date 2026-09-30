package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeDate(t *testing.T) {
	cases := map[string]string{
		"":                                    "",
		"2026-09-30":                          "2026-09-30",
		"2026-09-30T10:00:00Z":                "2026-09-30",
		"2026-09-30 10:00:00.123 +0800 CST":   "2026-09-30",
		"2026-09-30 10:00:00 +0000 UTC":       "2026-09-30",
		"not a date":                          "not a date",
		"2026-09-30T10:00:00+08:00":           "2026-09-30",
		"2026-09-30 10:00:00.123456789 +0800": "2026-09-30",
	}
	for in, want := range cases {
		if got := normalizeDate(in); got != want {
			t.Errorf("normalizeDate(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJSONBArgAbsentIsSQLNull(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, json.RawMessage("null"), json.RawMessage(" null ")} {
		if got := jsonbArg(raw); got != nil {
			t.Errorf("jsonbArg(%q) = %#v, want untyped nil", raw, got)
		}
	}
	if got := jsonbArg(json.RawMessage(`{"rate":31.5}`)); got != `{"rate":31.5}` {
		t.Errorf("jsonbArg(object) = %#v", got)
	}
}

func TestJSONBValueNullIsOmitted(t *testing.T) {
	if jsonbValue(nil) != nil || jsonbValue([]byte("null")) != nil {
		t.Error("SQL NULL / JSONB null must map to a nil RawMessage")
	}
	if string(jsonbValue([]byte(`{"a":1}`))) != `{"a":1}` {
		t.Error("object must be returned verbatim")
	}
}

func TestCheckJSONObject(t *testing.T) {
	for _, ok := range []string{"", "null", `{}`, ` {"amount":1,"currency":"JPY"}`} {
		if err := checkJSONObject("f", json.RawMessage(ok)); err != nil {
			t.Errorf("checkJSONObject(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{`"TWD"`, `1`, `[]`, `true`} {
		if err := checkJSONObject("f", json.RawMessage(bad)); err == nil {
			t.Errorf("checkJSONObject(%q) = nil, want error", bad)
		}
	}
}

// Decode a push-shaped body and re-encode it as a pull would: new optional
// fields must round-trip when present and stay absent when omitted.
func TestSyncStructsOptionalFieldsRoundTrip(t *testing.T) {
	const withFields = `{"id":"r1","bookId":"b1","type":"expense","amount":100,"category":"Food","date":"2026-09-30","note":"","paidById":"m1","splitAmongIds":["m1"],"splitCustomAmounts":{"m1":100},"amountCurrency":"TWD","original":{"amount":500,"currency":"JPY"},"booked":{"amount":100,"currency":"TWD","rate":0.2,"rateDate":"2026-09-30","rateSource":"manual"},"fx":{"rate":0.2,"rateDate":"2026-09-30","source":"manual"}}`
	var rec SyncRecord
	if err := json.Unmarshal([]byte(withFields), &rec); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(rec)
	if string(out) != withFields {
		t.Errorf("round trip mismatch:\n got %s\nwant %s", out, withFields)
	}

	const legacy = `{"id":"r1","bookId":"b1","type":"expense","amount":100,"category":"Food","date":"2026-09-30","note":"","paidById":"m1","splitAmongIds":["m1"]}`
	rec = SyncRecord{}
	if err := json.Unmarshal([]byte(legacy), &rec); err != nil {
		t.Fatal(err)
	}
	out, _ = json.Marshal(rec)
	if string(out) != legacy {
		t.Errorf("legacy record gained fields:\n got %s\nwant %s", out, legacy)
	}

	out, _ = json.Marshal(SyncData{})
	for _, k := range []string{"profile", "baseCurrency"} {
		if strings.Contains(string(out), k) {
			t.Errorf("empty SyncData must omit %s: %s", k, out)
		}
	}
	cur := "TWD"
	out, _ = json.Marshal(SyncData{Profile: &SyncProfile{BaseCurrency: &cur}})
	if !strings.Contains(string(out), `"profile":{"baseCurrency":"TWD"}`) {
		t.Errorf("profile not encoded: %s", out)
	}
	out, _ = json.Marshal(SyncBook{ID: "b1", Members: []SyncMember{}})
	if strings.Contains(string(out), "currency") {
		t.Errorf("book without currency must omit it: %s", out)
	}
}
