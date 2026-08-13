//go:build linux

package audio

func ffmpegInputArgs(device string) []string {
	return []string{"-f", "pulse", "-ar", "48000", "-thread_queue_size", "1024", "-i", device}
}
