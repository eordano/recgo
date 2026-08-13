//go:build darwin

package audio

func GetDefaultOutput() (string, error) {
	return runTool("SwitchAudioSource", "-c", "-t", "output")
}

func SetDefaultOutput(name string) error {
	_, err := runTool("SwitchAudioSource", "-s", name, "-t", "output")
	return err
}
