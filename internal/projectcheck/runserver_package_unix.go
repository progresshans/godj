//go:build darwin || linux

package projectcheck

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// validateRunserverPackageBoundary closes Go command pattern/file-list
// interpretations and requires declaration and runtime to resolve through real
// project-root directories with different filesystem identities.
func validateRunserverPackageBoundary(project retainedProject) bool {
	declaration, declarationOK := retainedRunserverPackageDirectory(project.root, project.descriptor.packagePath)
	runtime, runtimeOK := retainedRunserverPackageDirectory(project.root, project.descriptor.runserverPackagePath)
	return declarationOK && runtimeOK && !sameIdentity(declaration, runtime)
}

func retainedRunserverPackageDirectory(root *os.File, packagePath string) (unix.Stat_t, bool) {
	if root == nil || !strings.HasPrefix(packagePath, "./") || len(packagePath) == 2 {
		return unix.Stat_t{}, false
	}
	current := root
	owned := false
	var identity unix.Stat_t
	for _, component := range strings.Split(packagePath[2:], "/") {
		next, nextIdentity, err := openDirectoryAt(current, component)
		if owned {
			if closeErr := current.Close(); closeErr != nil {
				if next != nil {
					_ = next.Close()
				}
				return unix.Stat_t{}, false
			}
		}
		if err != nil {
			return unix.Stat_t{}, false
		}
		current = next
		identity = nextIdentity
		owned = true
	}
	if !owned || current.Close() != nil {
		return unix.Stat_t{}, false
	}
	return identity, true
}
