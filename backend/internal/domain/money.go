package domain

import (
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

// Money represents a monetary amount in USD cents. It avoids floating-point
// rounding issues and is safe to compare / sum.
type Money int64

// NewMoneyFromDecimal converts a shopspring decimal dollar value to Money.
func NewMoneyFromDecimal(d decimal.Decimal) Money {
	return Money(d.Mul(decimal.NewFromInt(100)).Round(0).IntPart())
}

// NewMoneyFromFloat converts a float (dollars) to Money.
func NewMoneyFromFloat(v float64) Money {
	return NewMoneyFromDecimal(decimal.NewFromFloat(v))
}

// Cents returns the raw cent value.
func (m Money) Cents() int64 { return int64(m) }

// Dollars returns a decimal representation in dollars.
func (m Money) Dollars() decimal.Decimal {
	return decimal.New(int64(m), -2)
}

// Add returns m+n.
func (m Money) Add(n Money) Money { return m + n }

// Sub returns m-n.
func (m Money) Sub(n Money) Money { return m - n }

// MulDecimal returns m multiplied by a decimal factor, rounded to cents.
func (m Money) MulDecimal(d decimal.Decimal) Money {
	r := m.Dollars().Mul(d)
	return NewMoneyFromDecimal(r)
}

// IsNegative returns true if amount < 0.
func (m Money) IsNegative() bool { return m < 0 }

// IsZero reports zero amount.
func (m Money) IsZero() bool { return m == 0 }

// String returns a USD formatted representation, e.g. "$1,234.56".
func (m Money) String() string {
	return fmt.Sprintf("$%s", m.Dollars().StringFixed(2))
}

// Value implements sql/driver Valuer — stores as integer cents.
func (m Money) Value() (driver.Value, error) { return int64(m), nil }

// Scan implements sql.Scanner for integer columns.
func (m *Money) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*m = 0
	case int64:
		*m = Money(v)
	case int:
		*m = Money(v)
	case float64:
		*m = Money(v)
	case []byte:
		var i int64
		if _, err := fmt.Sscan(string(v), &i); err != nil {
			return fmt.Errorf("money: %w", err)
		}
		*m = Money(i)
	case string:
		var i int64
		if _, err := fmt.Sscan(v, &i); err != nil {
			return fmt.Errorf("money: %w", err)
		}
		*m = Money(i)
	default:
		return errors.New("money: unsupported scan type")
	}
	return nil
}
