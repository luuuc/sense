package container

type Stack[T any] struct {
	items []T
}

func (s *Stack[T]) Push(v T) {
	s.items = append(s.items, v)
}

func (s *Stack[T]) Pop() T {
	var zero T
	if len(s.items) == 0 {
		return zero
	}
	v := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1]
	return v
}

func New[T any]() *Stack[T] {
	return &Stack[T]{}
}
