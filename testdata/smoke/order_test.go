package smoke

import "testing"

func TestOrderProcess(t *testing.T) {
	s := &OrderService{}
	s.Process()
}
