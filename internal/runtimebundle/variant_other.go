//go:build !linux

package runtimebundle

func currentRuntimeVariant() (string, error) { return "", nil }
