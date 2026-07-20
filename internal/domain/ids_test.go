package domain

import "testing"

func TestValidStableID(t *testing.T) {
	valid := "invk_019b0a12-0000-7000-8000-000000000004"
	if !ValidStableID(valid, PrefixInvocation) {
		t.Fatal("valid Invocation ID rejected")
	}
	for _, invalid := range []string{
		"invk_019b0a12-0000-6000-8000-000000000004",
		"invk_019b0a12-0000-7000-7000-000000000004",
		"invk_019B0a12-0000-7000-8000-000000000004",
		"sesn_019b0a12-0000-7000-8000-000000000004",
	} {
		if ValidStableID(invalid, PrefixInvocation) {
			t.Fatalf("invalid ID accepted: %s", invalid)
		}
	}
}
