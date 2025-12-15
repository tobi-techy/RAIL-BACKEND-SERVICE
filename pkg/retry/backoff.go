package retry

import (
	"math"
	"math/rand"
	"time"
)

// Backoff calculates retry delays
type Backoff struct {
	policy Policy
	rng    *rand.Rand
}

// NewBackoff creates a new backoff calculator
func NewBackoff(policy Policy) *Backoff {
	return &Backoff{
		policy: policy,
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Calculate computes the delay for the given attempt number
func (b *Backoff) Calculate(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	
	// Base exponential backoff
	backoff := float64(b.policy.InitialBackoff) * math.Pow(b.policy.Multiplier, float64(attempt-1))
	
	// Apply max backoff cap
	if b.policy.MaxBackoff > 0 && backoff > float64(b.policy.MaxBackoff) {
		backoff = float64(b.policy.MaxBackoff)
	}
	
	// Apply jitter
	if b.policy.Jitter > 0 {
		jitter := backoff * b.policy.Jitter
		backoff = backoff - jitter + (b.rng.Float64() * 2 * jitter)
	}
	
	return time.Duration(backoff)
}

// CalculateExponential calculates exponential backoff without jitter
func CalculateExponential(initialBackoff time.Duration, multiplier float64, attempt int, maxBackoff time.Duration) time.Duration {
	if attempt <= 0 {
		return 0
	}
	
	backoff := float64(initialBackoff) * math.Pow(multiplier, float64(attempt-1))
	
	if maxBackoff > 0 && backoff > float64(maxBackoff) {
		backoff = float64(maxBackoff)
	}
	
	return time.Duration(backoff)
}

// CalculateLinear calculates linear backoff
func CalculateLinear(initialBackoff time.Duration, attempt int, maxBackoff time.Duration) time.Duration {
	if attempt <= 0 {
		return 0
	}
	
	backoff := initialBackoff * time.Duration(attempt)
	
	if maxBackoff > 0 && backoff > maxBackoff {
		backoff = maxBackoff
	}
	
	return backoff
}

// CalculateConstant returns constant backoff
func CalculateConstant(backoff time.Duration) time.Duration {
	return backoff
}

// AddJitter adds random jitter to a duration
func AddJitter(duration time.Duration, jitterFactor float64) time.Duration {
	if jitterFactor <= 0 || jitterFactor > 1.0 {
		return duration
	}
	
	jitter := float64(duration) * jitterFactor
	base := float64(duration) - jitter
	random := rand.Float64() * 2 * jitter
	
	return time.Duration(base + random)
}

// FullJitter applies full jitter (0 to maxBackoff)
func FullJitter(maxBackoff time.Duration) time.Duration {
	if maxBackoff <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(maxBackoff)))
}

// EqualJitter applies equal jitter (half base + half random)
func EqualJitter(baseBackoff time.Duration) time.Duration {
	if baseBackoff <= 0 {
		return 0
	}
	half := baseBackoff / 2
	return half + time.Duration(rand.Int63n(int64(half)))
}

// DecorrelatedJitter applies decorrelated jitter (prevents thundering herd)
func DecorrelatedJitter(previousBackoff, baseBackoff, maxBackoff time.Duration) time.Duration {
	if previousBackoff == 0 {
		previousBackoff = baseBackoff
	}
	
	// Random value between base and 3x previous backoff
	min := float64(baseBackoff)
	max := math.Min(float64(maxBackoff), float64(previousBackoff)*3)
	
	if max <= min {
		return time.Duration(min)
	}
	
	return time.Duration(min + rand.Float64()*(max-min))
}
