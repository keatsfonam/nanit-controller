package controller

import (
	"math"
	"math/rand"
	"time"
)

type exponentialBackoff struct {
	attempt int
	max     time.Duration
	jitter  float64
	rnd     *rand.Rand
}

func newExponentialBackoff(max time.Duration, jitter float64, rnd *rand.Rand) *exponentialBackoff {
	if max <= 0 {
		max = time.Second
	}
	if jitter < 0 {
		jitter = 0
	}
	if rnd == nil {
		rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &exponentialBackoff{max: max, jitter: jitter, rnd: rnd}
}

func (b *exponentialBackoff) Next(base time.Duration) time.Duration {
	if base <= 0 {
		base = time.Second
	}
	delay := multiplyDuration(base, b.attempt)
	if delay > b.max {
		delay = b.max
	}
	b.attempt++
	return b.withJitter(delay)
}

func (b *exponentialBackoff) Reset() {
	b.attempt = 0
}

func multiplyDuration(base time.Duration, attempt int) time.Duration {
	if attempt <= 0 {
		return base
	}
	if attempt >= 62 {
		return time.Duration(math.MaxInt64)
	}
	factor := int64(1) << attempt
	if int64(base) > math.MaxInt64/factor {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(int64(base) * factor)
}

func (b *exponentialBackoff) withJitter(delay time.Duration) time.Duration {
	if delay <= 0 || b.jitter <= 0 {
		return delay
	}
	// Uniform jitter in [1-jitter, 1+jitter].
	span := b.jitter * 2
	factor := 1 - b.jitter + b.rnd.Float64()*span
	if factor <= 0 {
		factor = 0.1
	}
	return time.Duration(float64(delay) * factor)
}
