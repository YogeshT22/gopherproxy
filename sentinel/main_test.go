package main

import (
	"os"
	"testing"
)

func TestGetEnv_Default(t *testing.T) {
	val := getEnv("SENTINEL_TEST_MISSING_VAR", "fallback")
	if val != "fallback" {
		t.Errorf("expected 'fallback', got %q", val)
	}
}

func TestGetEnv_FromEnvironment(t *testing.T) {
	t.Setenv("SENTINEL_TEST_VAR", "hello")
	val := getEnv("SENTINEL_TEST_VAR", "fallback")
	if val != "hello" {
		t.Errorf("expected 'hello', got %q", val)
	}
}

func TestGetTargets_Defaults(t *testing.T) {
	// Ensure env var is absent so we get the hard-coded defaults.
	os.Unsetenv("SENTINEL_TARGETS")
	targets := getTargets()
	if len(targets) != 3 {
		t.Fatalf("expected 3 default targets, got %d", len(targets))
	}
}

func TestGetTargets_FromEnv(t *testing.T) {
	t.Setenv("SENTINEL_TARGETS", "host1:8081, host2:8082 , host3:8083")
	targets := getTargets()
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}
	// Whitespace must be trimmed.
	for _, tgt := range targets {
		if tgt[0] == ' ' || tgt[len(tgt)-1] == ' ' {
			t.Errorf("target %q was not trimmed", tgt)
		}
	}
}

func TestGetTargets_SingleEntry(t *testing.T) {
	t.Setenv("SENTINEL_TARGETS", "myhost:9999")
	targets := getTargets()
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0] != "myhost:9999" {
		t.Errorf("expected 'myhost:9999', got %q", targets[0])
	}
}
