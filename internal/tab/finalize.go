package tab

import (
	"fmt"
	"os"
)

// FinalizeDir renames a provisional session directory to its final name.
// The final name has only minute precision plus a generated slug, so two
// sessions can want the same directory; instead of clobbering the earlier
// recording, the first free _2, _3, ... suffix is taken. The rename itself
// can still lose a race with a concurrent finalize, so a rename failure
// against a path that now exists moves on to the next candidate.
func FinalizeDir(provisional, want string) (string, error) {
	for n := 1; n <= 10000; n++ {
		cand := want
		if n > 1 {
			cand = fmt.Sprintf("%s_%d", want, n)
		}
		if _, err := os.Lstat(cand); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		err := os.Rename(provisional, cand)
		if err == nil {
			return cand, nil
		}
		if _, statErr := os.Lstat(cand); statErr == nil {
			continue // lost the race; try the next suffix
		}
		return "", err
	}
	return "", fmt.Errorf("finalize %s: no free name after 10000 attempts", want)
}
