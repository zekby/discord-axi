//go:build !unix

package app

import "os"

// ponytail: no advisory locking outside unix, so concurrent processes can pace
// slightly tighter than configured. Swap in LockFileEx if Windows matters.
func lockFile(*os.File) (func(), error) {
	return func() {}, nil
}
