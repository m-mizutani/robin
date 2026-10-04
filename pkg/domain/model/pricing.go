package model

import (
	"fmt"
	"math"

	"github.com/m-mizutani/goerr/v2"
)

// NanoUSD is an amount of money in units of 1e-9 USD. A price per token is
// below one micro dollar, so an integer of this unit adds costs without
// rounding errors.
type NanoUSD int64

const nanoPerUSD = 1_000_000_000

// FromUSD converts dollars.
func FromUSD(usd float64) NanoUSD {
	return NanoUSD(math.Round(usd * nanoPerUSD))
}

// FromUSDPerMTok converts a price in dollars per million tokens to the price
// of one token.
func FromUSDPerMTok(usd float64) NanoUSD {
	return NanoUSD(math.Round(usd * nanoPerUSD / 1_000_000))
}

// USD formats the amount rounded to cents, such as "$2.01".
func (x NanoUSD) USD() string {
	cents := int64(math.Round(float64(x) / (nanoPerUSD / 100)))
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100)
}

// Rate is the price of one token of each kind.
type Rate struct {
	Input      NanoUSD
	Output     NanoUSD
	CacheRead  NanoUSD
	CacheWrite NanoUSD
}

func (r Rate) Validate() error {
	if r.Input <= 0 {
		return goerr.New("input token price must be positive", goerr.V("input", r.Input))
	}
	if r.Output <= 0 {
		return goerr.New("output token price must be positive", goerr.V("output", r.Output))
	}
	if r.CacheRead < 0 {
		return goerr.New("cache read token price must not be negative", goerr.V("cache_read", r.CacheRead))
	}
	if r.CacheWrite < 0 {
		return goerr.New("cache write token price must not be negative", goerr.V("cache_write", r.CacheWrite))
	}
	return nil
}

// Cost prices one call. InputTokens of LLMUsage already excludes the cached
// tokens, so nothing is subtracted.
func (r Rate) Cost(u LLMUsage) NanoUSD {
	return NanoUSD(u.InputTokens)*r.Input +
		NanoUSD(u.CacheReadInputTokens)*r.CacheRead +
		NanoUSD(u.CacheCreationInputTokens)*r.CacheWrite +
		NanoUSD(u.OutputTokens)*r.Output
}
