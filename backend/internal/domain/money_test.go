package domain_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nulzo/trader/internal/domain"
)

func TestMoney_FromFloat(t *testing.T) {
	m := domain.NewMoneyFromFloat(12.34)
	assert.Equal(t, int64(1234), m.Cents())
	assert.Equal(t, "$12.34", m.String())
}

func TestMoney_Arithmetic(t *testing.T) {
	a := domain.NewMoneyFromFloat(10.50)
	b := domain.NewMoneyFromFloat(1.25)
	assert.Equal(t, domain.NewMoneyFromFloat(11.75), a.Add(b))
	assert.Equal(t, domain.NewMoneyFromFloat(9.25), a.Sub(b))
}

func TestMoney_MulDecimal(t *testing.T) {
	m := domain.NewMoneyFromFloat(100)
	out := m.MulDecimal(decimal.NewFromFloat(0.255))
	assert.Equal(t, domain.NewMoneyFromFloat(25.5), out)
}

func TestMoney_Scan(t *testing.T) {
	var m domain.Money
	require.NoError(t, m.Scan(int64(4321)))
	assert.Equal(t, int64(4321), m.Cents())
	require.NoError(t, m.Scan([]byte("999")))
	assert.Equal(t, int64(999), m.Cents())
	require.NoError(t, m.Scan("1234"))
	assert.Equal(t, int64(1234), m.Cents())
}

func TestMoney_Sign(t *testing.T) {
	assert.True(t, domain.NewMoneyFromFloat(-1).IsNegative())
	assert.True(t, domain.Money(0).IsZero())
}
