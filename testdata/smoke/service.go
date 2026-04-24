package smoke

type BaseService struct{}

func (b *BaseService) Log(msg string) {}

type Notifier interface {
	Notify(msg string)
}
