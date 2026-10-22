package core

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Amount bounds, derived from the storage column NUMERIC(30,18): at most 12
// integer digits and 18 fractional digits.
const (
	MaxAmountIntegerDigits    = 12
	MaxAmountFractionalDigits = 18

	// maxAmountExponentMagnitude bounds a negative exponent (trailing
	// fractional zeros, e.g. "1.000…0") before anything rescales it.
	maxAmountExponentMagnitude = 1000
)

// ValidateAmountMagnitude refuses an amount the storage column could not hold.
//
// shopspring/decimal keeps a value as coefficient x 10^exponent and only
// expands it when something asks for its digits, so "1E999999999" parses,
// compares and adds in microseconds and then hangs whatever renders it (the
// driver, binding it as a NUMERIC parameter). This check reads only the
// exponent and the coefficient's digit count, never the expanded value.
//
// (NaN and infinities cannot be represented by decimal.Decimal at all; the
// schema refuses NaN in the amount columns, see migration 022.)
func ValidateAmountMagnitude(d decimal.Decimal) error {
	exp := int64(d.Exponent())
	coeff := d.Coefficient()
	digits := int64(len(coeff.Text(10)))
	if coeff.Sign() < 0 {
		digits-- // the minus sign is not a digit
	}
	// The exponent is bounded FIRST, zero included: "0E999999999" is still a
	// billion digits to anything that renders or rescales it.
	if exp < -maxAmountExponentMagnitude || exp > MaxAmountIntegerDigits {
		return fmt.Errorf("core: amount exponent %d is out of range: %w", exp, ErrInvalidInput)
	}
	if coeff.Sign() == 0 {
		return nil
	}
	if exp < -MaxAmountFractionalDigits {
		// More fractional digits than storable, unless they are trailing zeros.
		if !d.Truncate(MaxAmountFractionalDigits).Equal(d) {
			return fmt.Errorf("core: amount has more than %d fractional digits: %w", MaxAmountFractionalDigits, ErrInvalidInput)
		}
	}
	// Integer digits = digits + exp (when exp < 0 some digits are fractional).
	if intDigits := digits + exp; intDigits > MaxAmountIntegerDigits {
		return fmt.Errorf("core: amount has more than %d integer digits: %w", MaxAmountIntegerDigits, ErrInvalidInput)
	}
	return nil
}
