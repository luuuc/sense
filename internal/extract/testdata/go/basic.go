package billing

import "fmt"

const Version = "1.0"

const (
	MaxRetries = 3
	MinDelay   = 100
)

type Money struct {
	Amount   int
	Currency string
}

type Processor interface {
	Process(m Money) error
}

type Handler = func(Money) error

type Amount int

func (m Money) Format() string {
	return fmt.Sprintf("%d %s", m.Amount, m.Currency)
}

func (m *Money) Add(other Money) {
	m.Amount += other.Amount
}

func Process(m Money) error {
	return nil
}
