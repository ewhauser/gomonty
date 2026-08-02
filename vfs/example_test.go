package vfs_test

import (
	"context"
	"fmt"

	monty "github.com/ewhauser/gomonty"
	"github.com/ewhauser/gomonty/vfs"
)

func ExampleHandler() {
	fileSystem := vfs.NewMemoryFS()
	fileSystem.AddText("/data/input.txt", "hello from memory")

	runner, err := monty.New(`
from pathlib import Path

Path("/data/input.txt").read_text()
`, monty.CompileOptions{
		ScriptName: "example.py",
	})
	if err != nil {
		panic(err)
	}

	value, err := runner.Run(context.Background(), monty.RunOptions{
		OS: vfs.Handler(fileSystem, vfs.MapEnvironment{
			"HOME": "/sandbox",
		}),
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(value.Raw())
	// Output: hello from memory
}
