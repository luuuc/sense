package smoke

type PaymentGateway struct{}

func (g *PaymentGateway) Charge(amount int) error {
	return nil
}

func (g *PaymentGateway) Refund(amount int) error {
	return nil
}
