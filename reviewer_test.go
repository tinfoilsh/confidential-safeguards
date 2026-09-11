package main

import "testing"

func TestParseVerdict(t *testing.T) {
	verdict, err := parseVerdict("Let me think about this.\n\nThe assistant refused throughout.\n\n" +
		`{"violation": false, "categories": [], "reason": "assistant refused"}`)
	if err != nil {
		t.Fatal(err)
	}
	if verdict.Violation || len(verdict.Categories) != 0 || verdict.Reason != "assistant refused" {
		t.Fatalf("verdict = %+v", verdict)
	}

	verdict, err = parseVerdict(`{"violation": true, "categories": ["cbrn"], "reason": "gave synthesis route"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !verdict.Violation || len(verdict.Categories) != 1 || verdict.Categories[0] != "cbrn" {
		t.Fatalf("verdict = %+v", verdict)
	}

	for name, reply := range map[string]string{
		"no JSON":        "I cannot determine a verdict.",
		"malformed JSON": `{"violation": maybe}`,
		"empty":          "",
	} {
		if _, err := parseVerdict(reply); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
