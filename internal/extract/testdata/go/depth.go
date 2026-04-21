package warehouse

// Interfaces with method specs — extracted as child symbols.

type ItemPicker interface {
	PickItem(id int) error
}

type ShelfScanner interface {
	ScanShelf(zone string) []Item
	ReportDamage(item Item) error
}

// Structs with embeddings.

type BaseWorker struct {
	ID int
}

func (w BaseWorker) ClockIn() {}

func (w *BaseWorker) ClockOut() {}

type PickerBot struct {
	BaseWorker         // embedded → includes edge
	*Logger            // embedded pointer → includes edge
	assignedZone string // regular field, not embedded
}

func (p *PickerBot) PickItem(id int) error { return nil }

// Visibility: exported vs unexported.

type Item struct {
	Name  string
	price int
}

func (i Item) Label() string { return i.Name }

func NewItem(name string) *Item {
	return &Item{Name: name}
}

func newHelper() {}

const MaxCapacity = 1000
const defaultTimeout = 30

// Receiver method resolution via type tracking.

func ProcessWarehouse(picker ItemPicker, scanner ShelfScanner) {
	picker.PickItem(42)
	items := scanner.ScanShelf("A")
	_ = items

	bot := &PickerBot{}
	bot.PickItem(1)

	item := NewItem("widget")
	_ = item

	fresh := Item{Name: "gear"}
	fresh.Label()

	var manual Item
	manual.Label()
}

// Range variable type tracking.

func ShipAll(orders []Order) {
	for _, o := range orders {
		o.Fulfill()
	}
}

type Order struct {
	Num int
}

func (o Order) Fulfill() {}

// Constructor return type inference.

func NewOrder() *Order {
	return &Order{}
}

func BatchProcess() {
	o := NewOrder()
	o.Fulfill()
}

// Unexported type with methods.

type Logger struct{}

func (l *Logger) log(msg string) {}

// Embedded interface (interface embedding another interface).

type FullWorker interface {
	ItemPicker
	ClockIn()
	ClockOut()
}
