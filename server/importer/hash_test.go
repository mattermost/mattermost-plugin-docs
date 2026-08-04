// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package importer

import "testing"

func TestHashSourceState_StableAcrossMapKeyOrder(t *testing.T) {
	a := SourceContentHashInput{
		Title:         "Title",
		CanonicalBody: `{"type":"doc"}`,
		SourceProps: map[string]any{
			"import_labels": []any{"x", "y"},
			"zzz":           "last",
			"aaa":           "first",
		},
	}
	b := SourceContentHashInput{
		Title:         "Title",
		CanonicalBody: `{"type":"doc"}`,
		SourceProps: map[string]any{
			"aaa":           "first",
			"zzz":           "last",
			"import_labels": []any{"x", "y"},
		},
	}
	ha, err := HashSourceContent(a)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := HashSourceContent(b)
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Errorf("hash not stable across map key order: %s != %s", ha, hb)
	}
	if !IsValidSHA256Hex(ha) {
		t.Errorf("hash not 64-hex: %q", ha)
	}
}

func TestHashSourceState_ChangesWithContent(t *testing.T) {
	base := SourceContentHashInput{Title: "A", CanonicalBody: `{"type":"doc"}`}
	changed := base
	changed.CanonicalBody = `{"type":"doc","content":[]}`
	h1, _ := HashSourceContent(base)
	h2, _ := HashSourceContent(changed)
	if h1 == h2 {
		t.Errorf("expected different hashes for different bodies")
	}
}

func TestHashAppliedState_Deterministic(t *testing.T) {
	in := AppliedContentHashInput{Title: "T", BodyFormat: BodyFormatCanonicalTipTap, Body: `{"type":"doc"}`}
	h1, _ := HashAppliedContent(in)
	h2, _ := HashAppliedContent(in)
	if h1 != h2 || !IsValidSHA256Hex(h1) {
		t.Errorf("applied hash not deterministic/valid: %s %s", h1, h2)
	}
}

func TestIsValidSHA256Hex(t *testing.T) {
	valid := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if !IsValidSHA256Hex(valid) {
		t.Errorf("expected valid")
	}
	for _, bad := range []string{"", "ABCDEF", valid + "0", valid[:63], "z" + valid[1:]} {
		if IsValidSHA256Hex(bad) {
			t.Errorf("expected invalid: %q", bad)
		}
	}
}
