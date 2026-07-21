package controller

import (
	"math/rand"
	"testing"
	"time"
)

func TestExponentialBackoffCapsAndResets(t *testing.T) {
	b := newExponentialBackoff(5*time.Second, 0, rand.New(rand.NewSource(1)))
	if got := b.Next(time.Second); got != time.Second {
		t.Fatalf("first delay=%s", got)
	}
	if got := b.Next(time.Second); got != 2*time.Second {
		t.Fatalf("second delay=%s", got)
	}
	if got := b.Next(time.Second); got != 4*time.Second {
		t.Fatalf("third delay=%s", got)
	}
	if got := b.Next(time.Second); got != 5*time.Second {
		t.Fatalf("capped delay=%s", got)
	}
	b.Reset()
	if got := b.Next(time.Second); got != time.Second {
		t.Fatalf("reset delay=%s", got)
	}
}

func TestExponentialBackoffJitterStaysWithinBounds(t *testing.T) {
	b := newExponentialBackoff(10*time.Second, 0.2, rand.New(rand.NewSource(1)))
	got := b.Next(5 * time.Second)
	if got < 4*time.Second || got > 6*time.Second {
		t.Fatalf("jittered delay %s outside expected bounds", got)
	}
	if got := b.Next(10 * time.Second); got > 10*time.Second {
		t.Fatalf("jittered delay %s exceeded configured maximum", got)
	}
}
