package monty_test

import (
	"context"
	"fmt"

	monty "github.com/ewhauser/gomonty"
)

func ExampleRunner_Run() {
	runner, err := monty.New("40 + 2", monty.CompileOptions{
		ScriptName: "example.py",
	})
	if err != nil {
		panic(err)
	}

	value, err := runner.Run(context.Background(), monty.RunOptions{})
	if err != nil {
		panic(err)
	}

	fmt.Println(value.Raw())
	// Output: 42
}
