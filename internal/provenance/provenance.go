// Package provenance implements the append-only, hash-chained JSONL log
// required by the trust gate (§7). Each entry commits to the sha256 of the
// previous raw line, making truncation or in-place edits detectable.
package provenance

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const genesis = "0000000000000000000000000000000000000000000000000000000000000000"

type Entry struct {
	Prev     string `json:"prev"`
	Time     string `json:"ts"`
	Event    string `json:"event"` // add | author | promote | demote | gc
	Tool     string `json:"tool"`
	Version  string `json:"version"`
	TierFrom *int   `json:"tier_from"`
	TierTo   *int   `json:"tier_to"`
	Actor    string `json:"actor"`
	Origin   string `json:"origin,omitempty"`
	SelfTest string `json:"self_test,omitempty"`
	Reviewer string `json:"reviewer,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func Tier(n int) *int { return &n }

func lineHash(line []byte) string {
	h := sha256.Sum256(line)
	return hex.EncodeToString(h[:])
}

func lastLine(path string) ([]byte, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var last []byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		last = append(last[:0], sc.Bytes()...)
	}
	return last, sc.Err()
}

// Append writes e to the log at path, setting Time and Prev. It is the only
// write operation: there is no update or delete.
func Append(path string, e Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	last, err := lastLine(path)
	if err != nil {
		return err
	}
	if last == nil {
		e.Prev = genesis
	} else {
		e.Prev = lineHash(last)
	}
	e.Time = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(raw, '\n'))
	return err
}

// VerifyChain re-walks the log and checks every entry's prev-hash commitment.
// Returns the number of entries verified.
func VerifyChain(path string) (int, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()

	prev := genesis
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return n, fmt.Errorf("entry %d: malformed JSON: %w", n+1, err)
		}
		if e.Prev != prev {
			return n, fmt.Errorf("entry %d (%s %s@%s): chain broken: prev=%s want=%s",
				n+1, e.Event, e.Tool, e.Version, e.Prev, prev)
		}
		prev = lineHash(append([]byte(nil), line...))
		n++
	}
	return n, sc.Err()
}
