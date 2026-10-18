package cost

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// MicrosPerUnit is the count of micros in one unit of a currency.
const MicrosPerUnit = 1_000_000

// MaxMicros caps the size of one amount: 10^15 micros, one billion units
// of a currency. A sum of MaxResources such amounts still fits in an
// int64.
const MaxMicros = 1_000_000_000_000_000

const (
	// currencyCodeLen is the length of an ISO 4217 code.
	currencyCodeLen = 3
	// defaultMinorUnits is the count of decimals of a currency that is not
	// in minorUnits.
	defaultMinorUnits = 2
	// microsDigits is the count of decimals that a micro amount holds.
	microsDigits = 6
	// decimalBase is the base of the decimal digits.
	decimalBase = 10
)

// MinorUnits returns the count of decimals of the minor unit of currency
// in ISO 4217: 0 for JPY, 3 for BHD, and 2 for a currency that the list
// of this function does not name.
func MinorUnits(currency string) int {
	const (
		noDecimals    = 0
		threeDecimals = 3
	)

	switch currency {
	case "BIF", "CLP", "DJF", "GNF", "ISK", "JPY", "KMF", "KRW",
		"PYG", "RWF", "UGX", "VND", "VUV", "XAF", "XOF", "XPF":
		return noDecimals
	case "BHD", "IQD", "JOD", "KWD", "LYD", "OMR", "TND":
		return threeDecimals
	default:
		return defaultMinorUnits
	}
}

// Money is an amount in one currency. Currency is an ISO 4217 code of
// three capital letters. Micros is the amount in millionths of one unit,
// so that a price per hour of a small resource keeps its digits. The
// server never converts between currencies.
type Money struct {
	Currency string
	Micros   int64
}

// Zero returns the amount 0 in currency.
func Zero(currency string) Money {
	return Money{Currency: currency, Micros: 0}
}

// ValidCurrency reports whether c has the form of an ISO 4217 code.
func ValidCurrency(c string) bool {
	if len(c) != currencyCodeLen {
		return false
	}

	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}

	return true
}

// ValidateCost returns ErrInvalid for an amount that cannot be a cost or
// a spend: a currency that is not an ISO 4217 code, an amount below 0, or
// an amount above MaxMicros.
func (m Money) ValidateCost() error {
	switch {
	case !ValidCurrency(m.Currency):
		return fmt.Errorf("%w: currency %q is not an ISO 4217 code", ErrInvalid, m.Currency)
	case m.Micros < 0:
		return fmt.Errorf("%w: amount %d is below 0", ErrInvalid, m.Micros)
	case m.Micros > MaxMicros:
		return fmt.Errorf("%w: amount %d is above %d micros", ErrInvalid, m.Micros, int64(MaxMicros))
	}

	return nil
}

// Add returns m plus o. It returns ErrCurrencyMismatch for two currencies,
// and ErrInvalid when the sum does not fit.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("%w: %s and %s", ErrCurrencyMismatch, m.Currency, o.Currency)
	}

	if (o.Micros > 0 && m.Micros > math.MaxInt64-o.Micros) || (o.Micros < 0 && m.Micros < math.MinInt64-o.Micros) {
		return Money{}, fmt.Errorf("%w: the sum is too large", ErrInvalid)
	}

	return Money{Currency: m.Currency, Micros: m.Micros + o.Micros}, nil
}

// Sub returns m minus o, with the errors of Add.
func (m Money) Sub(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("%w: %s and %s", ErrCurrencyMismatch, m.Currency, o.Currency)
	}

	if (o.Micros < 0 && m.Micros > math.MaxInt64+o.Micros) || (o.Micros > 0 && m.Micros < math.MinInt64+o.Micros) {
		return Money{}, fmt.Errorf("%w: the difference is too large", ErrInvalid)
	}

	return Money{Currency: m.Currency, Micros: m.Micros - o.Micros}, nil
}

// String returns the amount in the minor unit of its currency, rounded
// half away from zero: "EUR 31.20", "EUR -4.05", "JPY 3100" or
// "BHD 1.250". A console shows its own format for the locale of the
// person: this form is for logs, chat messages and plain text.
func (m Money) String() string {
	a := m.minor()

	sign := ""
	if a.neg && !a.zero() {
		sign = "-"
	}

	return m.Currency + " " + sign + a.format()
}

// Delta returns the amount as a change, with a sign: "+EUR 31.20",
// "-EUR 4.05", or "EUR 0.00" for no change after rounding.
func (m Money) Delta() string {
	a := m.minor()

	switch {
	case a.zero():
		return m.Currency + " " + a.format()
	case a.neg:
		return "-" + m.Currency + " " + a.format()
	default:
		return "+" + m.Currency + " " + a.format()
	}
}

// rounded is the size of an amount in whole units and in minor units,
// with its sign and the count of digits of its minor unit.
type rounded struct {
	neg    bool
	whole  uint64
	frac   uint64
	digits int
}

// minor returns the amount rounded half away from zero to the minor unit
// of its currency. It computes on the size as an uint64, so that every
// int64 amount, also math.MinInt64, has a result.
func (m Money) minor() rounded {
	digits := MinorUnits(m.Currency)

	size := uint64(m.Micros) //nolint:gosec // the two's complement is undone below for a negative amount
	neg := m.Micros < 0
	if neg {
		size = -size
	}

	step := pow10(microsDigits - digits)
	units := size/step + (size%step*2)/step //nolint:mnd // round half away from zero
	perUnit := pow10(digits)

	return rounded{neg: neg, whole: units / perUnit, frac: units % perUnit, digits: digits}
}

func (r rounded) zero() bool { return r.whole == 0 && r.frac == 0 }

func (r rounded) format() string {
	s := strconv.FormatUint(r.whole, decimalBase)
	if r.digits == 0 {
		return s
	}

	f := strconv.FormatUint(r.frac, decimalBase)

	return s + "." + strings.Repeat("0", r.digits-len(f)) + f
}

func pow10(n int) uint64 {
	p := uint64(1)
	for range n {
		p *= decimalBase
	}

	return p
}
