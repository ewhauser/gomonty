// Command gomonty manages explicit setup tasks for the GoMonty library.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	monty "github.com/ewhauser/gomonty"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "prepare" {
		return errors.New("usage: gomonty prepare <download|build> [flags]")
	}
	if len(args) < 2 {
		return errors.New("prepare mode is required: download or build")
	}
	mode := monty.PrepareMode(args[1])
	if mode != monty.PrepareDownload && mode != monty.PrepareBuild {
		return fmt.Errorf("unknown prepare mode %q; use download or build", args[1])
	}
	flags := flag.NewFlagSet("gomonty prepare "+args[1], flag.ContinueOnError)
	flags.SetOutput(stderr)
	source := flags.String("source", "", "GoMonty source directory (build only)")
	baseURL := flags.String("base-url", "", "HTTPS release mirror (download only)")
	jsonOutput := flags.Bool("json", false, "print machine-readable result")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if mode == monty.PrepareDownload && *source != "" {
		return errors.New("--source is only valid with prepare build")
	}
	if mode == monty.PrepareBuild && *baseURL != "" {
		return errors.New("--base-url is only valid with prepare download")
	}
	result, err := monty.Prepare(ctx, monty.PrepareOptions{
		Mode:           mode,
		SourceDir:      *source,
		ReleaseBaseURL: *baseURL,
	})
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	_, err = fmt.Fprintf(stdout, "prepared %s for %s with %s\n", result.RuntimeVersion, result.Target, result.Mode)
	return err
}
