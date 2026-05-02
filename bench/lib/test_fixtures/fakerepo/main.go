package main

import "fmt"

type Engine struct{}

func (e *Engine) ServeHTTP() {
	fmt.Println("serving")
	e.handleRequest()
}

func (e *Engine) handleRequest() {
	fmt.Println("handling")
}

func unusedHelper() {
	fmt.Println("never called")
}

func main() {
	e := &Engine{}
	e.ServeHTTP()
}
