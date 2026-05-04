package dispatch

type Task struct {
	ID int
}

func (t Task) Execute() {}

func RunAll() {
	tasks := []Task{{ID: 1}, {ID: 2}}
	for _, t := range tasks {
		t.Execute()
	}

	for _, t := range []Task{{ID: 3}} {
		t.Execute()
	}

	var items []Task
	for _, item := range items {
		item.Execute()
	}
}
