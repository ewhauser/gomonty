package main

import (
	"context"
	"fmt"
	"log"

	monty "github.com/ewhauser/gomonty"
)

func main() {
	runner, err := monty.New("40 + 2", monty.CompileOptions{
		ScriptName: "example.py",
	})
	if err != nil {
		log.Fatal(err)
	}

	value, err := runner.Run(context.Background(), monty.RunOptions{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(value.Raw())
}
