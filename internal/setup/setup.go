//go:build windows

// Package setup implements the install and uninstall actions shared by
// Setup_StayWakeBlackScreenIdle.exe: downloading and registering
// StayWakeBlackScreenIdle.exe for autostart, and reversing that -
// removing the autostart entry, stopping any running copy, and deleting
// the installed files.
package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	repoOwner = "SpikePy"
	repoName  = "Windows-StayWakeBlackScreen"

	// idleAssetName and mainAssetName are the release asset / installed
	// exe names for the two blackout programs. Only the idle variant is
	// ever downloaded and autostarted; the plain variant is left as a
	// manual tool, but Uninstall still stops it if it happens to be
	// running.
	idleAssetName = "StayWakeBlackScreenIdle.exe"
	mainAssetName = "StayWakeBlackScreen.exe"

	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "StayWakeBlackScreenIdle"

	userAgent = "stay-wake-setup"
)

// resolveInstallDir returns dir, or %LOCALAPPDATA%\StayWakeBlackScreen if
// dir is empty.
func resolveInstallDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		return "", fmt.Errorf("%%LOCALAPPDATA%% is not set")
	}
	return filepath.Join(base, "StayWakeBlackScreen"), nil
}

type ghAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// InstallOptions configures Install.
type InstallOptions struct {
	InstallDir  string // defaults to %LOCALAPPDATA%\StayWakeBlackScreen if empty
	GitHubToken string // optional, avoids the unauthenticated API rate limit
	NoLaunch    bool   // install/update without starting it now
	NoAutostart bool   // don't register (or update) the autostart entry
}

// Install downloads the latest released StayWakeBlackScreenIdle.exe,
// installs it under the current user's %LOCALAPPDATA%, registers it to
// autostart at login, and (re)starts it - terminating any already-running
// copy first so the file can be replaced and so at most one copy is ever
// running at a time. Safe to re-run to update in place: it always ends up
// with exactly one autostart registry entry (a single named value, so
// re-running never adds a duplicate) and exactly one running instance
// (the app itself also refuses to start a second copy via a named mutex -
// see internal/singleinstance - so this is belt and suspenders).
func Install(opts InstallOptions) error {
	installDir, err := resolveInstallDir(opts.InstallDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("creating install dir: %w", err)
	}
	targetPath := filepath.Join(installDir, idleAssetName)

	fmt.Printf("Looking up latest release of %s/%s...\n", repoOwner, repoName)
	rel, err := latestRelease(opts.GitHubToken)
	if err != nil {
		return fmt.Errorf("fetching latest release: %w", err)
	}
	var downloadURL string
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, idleAssetName) {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("release %s has no asset named %s", rel.TagName, idleAssetName)
	}
	fmt.Printf("Downloading %s (%s)...\n", rel.TagName, downloadURL)

	tmpPath := targetPath + ".download"
	if err := downloadFile(downloadURL, tmpPath); err != nil {
		return fmt.Errorf("downloading asset: %w", err)
	}

	fmt.Println("Stopping any already-running instance...")
	if err := terminateRunning(idleAssetName); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("stopping running instance: %w", err)
	}

	fmt.Printf("Installing to %s...\n", targetPath)
	if err := replaceFile(tmpPath, targetPath); err != nil {
		return fmt.Errorf("installing: %w", err)
	}

	if !opts.NoAutostart {
		fmt.Println("Registering autostart...")
		if err := setAutostart(targetPath); err != nil {
			return fmt.Errorf("registering autostart: %w", err)
		}
	}

	if !opts.NoLaunch {
		fmt.Println("Starting it now...")
		if err := exec.Command(targetPath).Start(); err != nil {
			return fmt.Errorf("starting %s: %w", targetPath, err)
		}
	}

	fmt.Println("Done.")
	return nil
}

// UninstallOptions configures Uninstall.
type UninstallOptions struct {
	InstallDir string // defaults to %LOCALAPPDATA%\StayWakeBlackScreen if empty
	KeepFiles  bool   // remove autostart and stop the process, but leave the installed files in place
}

// Uninstall reverses Install: removes the autostart registry entry,
// terminates any running copy of StayWakeBlackScreenIdle.exe or
// StayWakeBlackScreen.exe, and (unless KeepFiles) deletes the installed
// files.
func Uninstall(opts UninstallOptions) error {
	installDir, err := resolveInstallDir(opts.InstallDir)
	if err != nil {
		return err
	}

	fmt.Println("Removing autostart entry...")
	if err := removeAutostart(); err != nil {
		return fmt.Errorf("removing autostart: %w", err)
	}

	fmt.Println("Stopping any running instance...")
	for _, exe := range []string{idleAssetName, mainAssetName} {
		if err := terminateRunning(exe); err != nil {
			return fmt.Errorf("stopping %s: %w", exe, err)
		}
	}

	if !opts.KeepFiles {
		fmt.Printf("Removing %s...\n", installDir)
		if err := os.RemoveAll(installDir); err != nil {
			return fmt.Errorf("removing %s: %w", installDir, err)
		}
	}

	fmt.Println("Done.")
	return nil
}

// latestRelease looks up the newest release through the GitHub API. The
// token, if any, is only ever sent here: it's what the API rate limit
// applies to, and the asset download itself needs no authentication.
func latestRelease(token string) (*ghRelease, error) {
	headers := []string{"Accept: application/vnd.github+json"}
	if token != "" {
		headers = append(headers, "Authorization: Bearer "+token)
	}
	var body bytes.Buffer
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	if err := httpGet(url, headers, &body); err != nil {
		return nil, fmt.Errorf("GitHub API: %w", err)
	}
	var rel ghRelease
	if err := json.Unmarshal(body.Bytes(), &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// downloadFile saves url's content to destPath, removing the file again
// if the download fails partway.
func downloadFile(url, destPath string) error {
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if err := httpGet(url, nil, out); err != nil {
		out.Close()
		os.Remove(destPath)
		return err
	}
	return out.Close()
}

// replaceFile moves tmpPath onto targetPath, retrying briefly: the target
// may still be momentarily locked right after terminateRunning killed the
// process that had it open/mapped.
func replaceFile(tmpPath, targetPath string) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = os.Rename(tmpPath, targetPath); err == nil {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	os.Remove(tmpPath)
	return err
}

func setAutostart(targetPath string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(runValueName, `"`+targetPath+`"`)
}

func removeAutostart() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
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
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), exeName) {
			pids = append(pids, entry.ProcessID)
		}
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
