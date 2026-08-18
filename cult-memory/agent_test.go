package main

import (
	"math"
	"testing"
)

func TestLocalEmbeddingIsNormalized256D(t *testing.T) {
	v := localEmbedding("Remember that our production API uses Go")
	if len(v) != 256 {
		t.Fatalf("len=%d, want 256", len(v))
	}
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	if math.Abs(math.Sqrt(sum)-1) > 1e-9 {
		t.Fatalf("embedding norm=%f, want 1", math.Sqrt(sum))
	}
}

func TestDurableCandidateRejectsSecrets(t *testing.T) {
	if isDurableCandidate("Remember my API key is abc123 and use it later") {
		t.Fatal("secret-bearing text must not become a memory proposal")
	}
	if !isDurableCandidate("Remember that our production API uses Go") {
		t.Fatal("durable project fact should be eligible for approval")
	}
}

func TestLocalRespondUsesOnlyRecalledApprovedContext(t *testing.T) {
	got := localRespond("What do you remember about our stack?", []memory{{Content: "Our production API uses Go", Approved: true}})
	if got.Answer == "" {
		t.Fatal("expected a response")
	}
	if got.ShouldRemember {
		t.Fatal("question alone should not create a new durable memory")
	}
}
