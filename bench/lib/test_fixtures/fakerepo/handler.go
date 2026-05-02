package main

import "fmt"

func routeRequest() {
	e := &Engine{}
	e.ServeHTTP()
	fmt.Println("routed")
}
