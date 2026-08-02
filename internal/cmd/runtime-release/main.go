package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/ewhauser/gomonty/internal/runtimebundle"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: runtime-release <generate|verify|verify-input|inspect-input> [flags]")
	}
	switch args[0] {
	case "generate":
		flags := flag.NewFlagSet("generate", flag.ContinueOnError)
		manifestPath := flags.String("manifest", "internal/runtimebundle/manifests/current.json", "manifest path")
		inputRoot := flags.String("input", "internal/ffi/lib", "native artifact input root")
		outputRoot := flags.String("output", ".release-assets", "release asset output root")
		sourceRoot := flags.String("source", ".", "native source root")
		releaseBaseURL := flags.String("release-base-url", "", "override manifest release base URL")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		manifestBytes, err := os.ReadFile(*manifestPath)
		if err != nil {
			return err
		}
		manifest, err := runtimebundle.ParseManifest(manifestBytes)
		if err != nil {
			return err
		}
		if *releaseBaseURL != "" {
			manifest.ReleaseBaseURL = *releaseBaseURL
		}
		manifest.SourceSHA256, err = runtimebundle.ComputeNativeSourceDigest(*sourceRoot)
		if err != nil {
			return err
		}
		manifest, err = runtimebundle.GenerateReleaseAssets(manifest, *inputRoot, *outputRoot)
		if err != nil {
			return err
		}
		if err := runtimebundle.WriteManifest(*manifestPath, manifest); err != nil {
			return err
		}
		return runtimebundle.VerifyReleaseAssets(manifest, *outputRoot)
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		manifestPath := flags.String("manifest", "internal/runtimebundle/manifests/current.json", "manifest path")
		assetRoot := flags.String("assets", ".release-assets", "release asset root")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		manifestBytes, err := os.ReadFile(*manifestPath)
		if err != nil {
			return err
		}
		manifest, err := runtimebundle.ParseManifest(manifestBytes)
		if err != nil {
			return err
		}
		return runtimebundle.VerifyReleaseAssets(manifest, *assetRoot)
	case "verify-input":
		flags := flag.NewFlagSet("verify-input", flag.ContinueOnError)
		manifestPath := flags.String("manifest", "internal/runtimebundle/manifests/current.json", "manifest path")
		inputRoot := flags.String("input", "internal/ffi/lib", "native artifact input root")
		target := flags.String("target", "", "single target ID (empty verifies all)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		manifestBytes, err := os.ReadFile(*manifestPath)
		if err != nil {
			return err
		}
		manifest, err := runtimebundle.ParseManifest(manifestBytes)
		if err != nil {
			return err
		}
		if *target != "" {
			return runtimebundle.VerifyInputTarget(manifest, *inputRoot, *target)
		}
		return runtimebundle.VerifyInputFiles(manifest, *inputRoot)
	case "inspect-input":
		flags := flag.NewFlagSet("inspect-input", flag.ContinueOnError)
		manifestPath := flags.String("manifest", "internal/runtimebundle/manifests/current.json", "manifest path")
		inputRoot := flags.String("input", "internal/ffi/lib", "native artifact input root")
		target := flags.String("target", "", "target ID")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *target == "" {
			return fmt.Errorf("inspect-input requires --target")
		}
		manifestBytes, err := os.ReadFile(*manifestPath)
		if err != nil {
			return err
		}
		manifest, err := runtimebundle.ParseManifest(manifestBytes)
		if err != nil {
			return err
		}
		files, err := runtimebundle.InspectInputTarget(manifest, *inputRoot, *target)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(files)
	default:
		return fmt.Errorf("unknown runtime-release command %q", args[0])
	}
}
