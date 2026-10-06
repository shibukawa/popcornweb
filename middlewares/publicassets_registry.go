package middlewares

import (
	"io/fs"
	"sync"
)

var publicFSState = struct {
	sync.RWMutex
	value fs.FS
}{}

// RegisterPublicFS installs the application's embedded public filesystem.
// Generated project public.go files call this during package initialization.
func RegisterPublicFS(value fs.FS) {
	if value == nil {
		panic("popcornweb: nil public filesystem")
	}
	publicFSState.Lock()
	defer publicFSState.Unlock()
	if publicFSState.value != nil {
		panic("popcornweb: public filesystem is already registered")
	}
	publicFSState.value = value
}

func registeredPublicFS() fs.FS {
	publicFSState.RLock()
	defer publicFSState.RUnlock()
	return publicFSState.value
}

// RegisteredPublicFS returns the application's embedded public filesystem, or
// nil where none was registered.
//
// It is exported for the other runtime, which serves the same tree and has to
// find it the same way: a generated public.go registers here whichever runtime
// the binary links, and a runtime that only looked at what it was handed served
// nothing at all.
func RegisteredPublicFS() fs.FS { return registeredPublicFS() }

// SwapPublicFS installs a filesystem and returns what was there. Passing nil
// clears it.
//
// It exists for tests, which install a tree and must put back what they found;
// RegisterPublicFS admits one registration and no undo, which is right for an
// init and unusable for a test.
func SwapPublicFS(value fs.FS) fs.FS {
	publicFSState.Lock()
	defer publicFSState.Unlock()
	previous := publicFSState.value
	publicFSState.value = value
	return previous
}
