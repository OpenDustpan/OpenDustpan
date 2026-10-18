package cost

import (
	"fmt"
	"math"
	"strconv"
)

// MicrosPerUnit is the count of micros in one unit of a currency.
const MicrosPerUnit = 1_000_000

const (
	// currencyCodeLen is the length of an ISO 4217 code.
	currencyCodeLen = 3
	// centsPerUnit is the count of hundredths in one unit of a currency.
	centsPerUnit = 100
)

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
	if o.Micros == math.MinInt64 {
		return Money{}, fmt.Errorf("%w: the amount is too large", ErrInvalid)
	}

	return m.Add(Money{Currency: o.Currency, Micros: -o.Micros})
}

// String returns the amount with two decimals, for example "EUR 31.20" or
// "EUR -4.05". A console shows its own format for the locale of the
// person: this form is for logs, chat messages and plain text.
func (m Money) String() string {
	c := m.cents()

	sign := ""
	if c < 0 {
		sign = "-"
		c = -c
	}

	return m.Currency + " " + sign + strconv.FormatInt(c/centsPerUnit, 10) + "." + fmt.Sprintf("%02d", c%centsPerUnit)
}

// Delta returns the amount as a change, with a sign: "+EUR 31.20",
// "-EUR 4.05", or "EUR 0.00" for no change after rounding.
func (m Money) Delta() string {
	c := m.cents()

	switch {
	case c > 0:
		return "+" + m.String()
	case c < 0:
		return "-" + Money{Currency: m.Currency, Micros: -m.Micros}.String()
	default:
		return Money{Currency: m.Currency, Micros: 0}.String()
	}
}

// cents returns the amount in hundredths, rounded half away from zero.
func (m Money) cents() int64 {
	const microsPerCent = MicrosPerUnit / centsPerUnit

	c := m.Micros / microsPerCent
	if r := m.Micros % microsPerCent; r >= microsPerCent/2 {
		c++
	} else if r <= -microsPerCent/2 {
		c--
	}

	return c
}
