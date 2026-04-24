package smoke

type OrderService struct {
	BaseService
}

func (s *OrderService) Process() {
	g := &PaymentGateway{}
	g.Charge(100)
}

func (s *OrderService) Notify(msg string) {}
