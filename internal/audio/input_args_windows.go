//go:build windows

package audio

func ffmpegInputArgs(device string) []string {
	return []string{"-thread_queue_size", "1024", "-f", "dshow", "-i", "audio=" + device}
}

func ffmpegMonitorInputArgs(device string) []string { return ffmpegInputArgs(device) }
