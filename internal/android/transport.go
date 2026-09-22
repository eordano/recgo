package android

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/eordano/recgo/internal/tab"
	"net"
	"time"
)

// Android rebinds accessibility services asynchronously after installation and
// after UIAutomator releases automation. Retry only the pre-capture handshake.
func connectHelper(ctx context.Context, address string, packages []string, clock *tab.Clock) (net.Conn, *bufio.Scanner, Event, float64, float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var last error
	for ctx.Err() == nil {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", address)
		if err == nil {
			deadline, _ := ctx.Deadline()
			_ = conn.SetDeadline(deadline)
			start := clock.Now()
			err = json.NewEncoder(conn).Encode(map[string]any{"version": 1, "packages": packages})
			scanner := bufio.NewScanner(conn)
			scanner.Buffer(make([]byte, 4096), 1<<20)
			if err == nil && scanner.Scan() {
				end := clock.Now()
				var hello Event
				if err = json.Unmarshal(scanner.Bytes(), &hello); err == nil && hello.Kind == "hello" && hello.Version == 1 && hello.ElapsedMs >= 0 {
					_ = conn.SetDeadline(time.Time{})
					return conn, scanner, hello, (start + end) / 2, (end-start)/2 + 1, nil
				}
				conn.Close()
				return nil, nil, Event{}, 0, 0, fmt.Errorf("incompatible helper handshake; update the helper APK")
			}
			if err == nil {
				err = scanner.Err()
				if err == nil {
					err = fmt.Errorf("device helper socket not ready")
				}
			}
			conn.Close()
		}
		last = err
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return nil, nil, Event{}, 0, 0, fmt.Errorf("helper unavailable: %v; run --helper-apk PATH --setup, enable Recgo accessibility and retry", last)
}
