//go:build windows

package protect

import "os"

// Windows has no Unix modes: os.Chmod only toggles the read-only attribute, and
// Stat reports a synthetic 0666/0777. Access is decided by the ACL, which a new
// file inherits from its directory. Under %AppData% (the user's profile) that is
// the user, SYSTEM and Administrators. So: no chmod, and no mode check.
// A file written elsewhere (`id new --ci --out C:\somewhere`) inherits that
// folder's ACL; the command should print a reminder to check it with icacls.
func chmodPrivate(path string, mode os.FileMode) error { return nil }

func looseMode(path string) (bool, os.FileMode) { return false, 0 }
