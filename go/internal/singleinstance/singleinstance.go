//go:build windows

// Package singleinstance guarantees at most one running copy of a program
// per user session via a named Win32 mutex - independent of, and a
// belt-and-suspenders complement to, the installer's own
// terminate-before-replace logic.
package singleinstance

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modKernel32      = windows.NewLazySystemDLL("kernel32.dll")
	procCreateMutex  = modKernel32.NewProc("CreateMutexW")
	procReleaseMutex = modKernel32.NewProc("ReleaseMutex")
	procCloseHandle  = modKernel32.NewProc("CloseHandle")
)

const errorAlreadyExists syscall.Errno = 183

// Acquire tries to become the sole running instance identified by name (a
// process-unique string; it is namespaced as a Local\ kernel object, so it
// only guards against other instances in the same login session, which is
// what "don't run twice for this user" needs).
//
// If another instance already holds it, alreadyRunning is true and release
// is nil. Otherwise release must be called (typically via defer) to give up
// the lock before the process exits; letting the process die without
// calling it also releases the mutex, since Windows abandons mutexes held
// by a terminated process.
func Acquire(name string) (release func(), alreadyRunning bool, err error) {
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	h, _, callErr := procCreateMutex.Call(0, 1 /* bInitialOwner */, uintptr(unsafe.Pointer(namePtr)))
	if h == 0 {
		return nil, false, callErr
	}
	handle := syscall.Handle(h)
	if errno, ok := callErr.(syscall.Errno); ok && errno == errorAlreadyExists {
		procCloseHandle.Call(uintptr(handle))
		return nil, true, nil
	}
	release = func() {
		procReleaseMutex.Call(uintptr(handle))
		procCloseHandle.Call(uintptr(handle))
	}
	return release, false, nil
}
