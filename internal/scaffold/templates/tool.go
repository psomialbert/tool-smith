// {{NAME}} — {{DESC}}
//
// Agent-friendly contract: --json machine output, --quiet, non-interactive,
// errors to stderr, exit codes: 0 ok / 1 runtime error / 2 usage error.
//
// Stdlib only (built with GOPROXY=off). If you add imports, the offline build
// at `smith use` / `smith install` time will fail until the module story is
// designed. Reviewers: check for `//go:embed` and `import "C"` — these read
// files / run cgo at BUILD time, which runs unsandboxed during adoption.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

const version = "{{VERSION}}"

func main() {
	jsonOut := flag.Bool("json", false, "machine-readable JSON on stdout")
	quiet := flag.Bool("quiet", false, "suppress non-essential human output")
	showVer := flag.Bool("version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "{{NAME}} %s — {{DESC}}\n\nUsage: {{NAME}} [--json] [--quiet] [--version] [args...]\n\nExit codes: 0 ok, 1 runtime error, 2 usage error\n", version)
	}
	flag.Parse()
	if *showVer {
		fmt.Println(version)
		return
	}

	// emit is the single choke point: the machine path is always structured.
	emit := func(machine any, human string) {
		if *jsonOut {
			b, _ := json.Marshal(machine)
			fmt.Println(string(b))
		} else if !*quiet {
			fmt.Println(human)
		}
	}

	// TODO: implement {{NAME}}. Replace this placeholder.
	args := flag.Args()
	emit(map[string]any{"ok": true, "args": len(args)}, fmt.Sprintf("ok (%d args)", len(args)))
}
