package tokens

const A, B = 1, 2

const (
	TokenAdd = iota
	TokenSub
	TokenMul
)

type Kind int
