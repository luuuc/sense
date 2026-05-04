package collection

import "io"

type Comparable[T any] struct{}

type Sortable interface {
	Less(other Sortable) bool
}

type Box struct {
	Comparable[int]
	io.Reader
}

type Wrapper struct {
	*Box
}
