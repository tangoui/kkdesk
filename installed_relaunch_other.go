//go:build !windows || !amd64 || !cgo

package main

func newInstalledAppPlatform() installedAppPlatform { return nil }

func waitForInstalledParent(args []string) ([]string, bool, error) { return args, false, nil }
