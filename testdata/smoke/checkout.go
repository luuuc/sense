package smoke

type CheckoutService struct {
	BaseService
}

func (c *CheckoutService) Run() {
	o := &OrderService{}
	o.Process()
}

func (c *CheckoutService) Notify(msg string) {}

type ShippingService struct {
	BaseService
}

func (s *ShippingService) Ship() {
	o := &OrderService{}
	o.Process()
}

func (s *ShippingService) Notify(msg string) {}
