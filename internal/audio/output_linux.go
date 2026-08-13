//go:build linux

package audio

func GetDefaultOutput() (string, error) { return "", nil }

func SetDefaultOutput(name string) error { return nil }
