//go:build windows

// Command stay-wake-uninstall reverses what stay-wake-install did: it
// removes the autostart registry entry, terminates any running copy of
// StayWakeBlackScreenIdle.exe/StayWakeBlackScreen.exe, and deletes the
// installed files.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	idleAssetName = "StayWakeBlackScreenIdle.exe"
	mainAssetName = "StayWakeBlackScreen.exe"

	runValueName = "StayWakeBlackScreenIdle"
)

func main() {
	installDir := flag.String("install-dir", "", "directory it was installed into (default: %LOCALAPPDATA%\\StayWakeBlackScreen)")
	keepFiles := flag.Bool("keep-files", false, "remove autostart and stop the process, but don't delete the installed files")
	flag.Parse()

	if err := run(*installDir, *keepFiles); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(installDir string, keepFiles bool) error {
	if installDir == "" {
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			return fmt.Errorf("%%LOCALAPPDATA%% is not set")
		}
		installDir = filepath.Join(base, "StayWakeBlackScreen")
	}

	fmt.Println("Removing autostart entry...")
	if err := removeAutostart(); err != nil {
		return fmt.Errorf("removing autostart: %w", err)
	}

	fmt.Println("Stopping any running instance...")
	if err := terminateRunning(idleAssetName); err != nil {
		return fmt.Errorf("stopping %s: %w", idleAssetName, err)
	}
	if err := terminateRunning(mainAssetName); err != nil {
		return fmt.Errorf("stopping %s: %w", mainAssetName, err)
	}

	if !keepFiles {
		fmt.Printf("Removing %s...\n", installDir)
		if err := os.RemoveAll(installDir); err != nil {
			return fmt.Errorf("removing %s: %w", installDir, err)
		}
	}

	fmt.Println("Done.")
	return nil
}

func removeAutostart() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer key.Close()

	if err := key.DeleteValue(runValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// terminateRunning finds every running process whose image file name
// matches exeName (case-insensitively) and terminates it, waiting briefly
// for each to actually exit.
func terminateRunning(exeName string) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	var pids []uint32
	err = windows.Process32First(snap, &entry)
	for err == nil {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(name, exeName) {
			pids = append(pids, entry.ProcessID)
		}
		err = windows.Process32Next(snap, &entry)
	}

	for _, pid := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			continue // already gone, or no permission - nothing more we can do
		}
		windows.TerminateProcess(h, 0)
		windows.WaitForSingleObject(h, 5000)
		windows.CloseHandle(h)
	}
	return nil
}
