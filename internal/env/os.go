package env

import "os"

// OS is the environment of this process. It is the only place under internal/
// that reads it; the command line calls it once, and a test calls it after
// t.Setenv.
func OS() Env { return New(os.Environ()) }
