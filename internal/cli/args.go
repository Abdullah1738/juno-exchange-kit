package cli

import (
	"flag"
	"strings"
)

// reorderFlagArgs lets flags appear after positional args by moving recognized
// flags (and their values) to the front.
//
// Go's stdlib flag parsing stops at the first non-flag argument. Many CLIs allow
// interspersed flags, so we normalize the argv for subcommands that take
// positionals.
func reorderFlagArgs(fs *flag.FlagSet, args []string) []string {
	type boolFlag interface{ IsBoolFlag() bool }

	var flags []string
	var pos []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i:]...)
			break
		}

		// "-" is used by some commands to mean stdin, so treat it as positional.
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)

			// "--flag=value" form.
			if strings.Contains(a, "=") {
				continue
			}

			name := strings.TrimLeft(a, "-")
			if name == "" {
				continue
			}
			f := fs.Lookup(name)
			if f == nil {
				continue
			}
			if bf, ok := f.Value.(boolFlag); ok && bf.IsBoolFlag() {
				continue
			}

			// Treat the next arg as the flag value when it doesn't look like a flag.
			if i+1 < len(args) {
				next := args[i+1]
				if next != "--" && !(strings.HasPrefix(next, "-") && next != "-") {
					flags = append(flags, next)
					i++
				}
			}
			continue
		}

		pos = append(pos, a)
	}

	return append(flags, pos...)
}
