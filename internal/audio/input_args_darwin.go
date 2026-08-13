//go:build darwin

package audio

func ffmpegInputArgs(device string) []string {
	return []string{"-thread_queue_size", "1024", "-f", "avfoundation", "-i", ":" + device}
}
