package audio

const SystemAudioStream = "recgo-system-audio"

func FFmpegInputArgs(device string) []string { return ffmpegInputArgs(device) }

func FFmpegMonitorInputArgs(device string) []string { return ffmpegMonitorInputArgs(device) }
