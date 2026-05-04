package chain

type Engine struct{}

func (e Engine) Start() {}

type Car struct {
	engine Engine
}

func Drive(c Car) {
	c.engine.Start()
}

func BuildAndDrive() {
	c := Car{}
	c.engine.Start()
	x := newEngine()
	_ = x
}

func newEngine() Engine {
	return Engine{}
}
