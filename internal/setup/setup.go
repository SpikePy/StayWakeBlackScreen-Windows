//go:build windows

// Package setup implements the install and uninstall actions shared by
// Setup_StayWakeBlackScreenIdle.exe: downloading and registering
// StayWakeBlackScreenIdle.exe for autostart, and reversing that -
// removing the autostart entry, stopping any running copy, and deleting
// the installed files.
package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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

	// IdleAssetName and MainAssetName are the release asset / installed
	// exe names for the two blackout programs. Only the idle variant is
	// ever downloaded and autostarted; the plain variant is left as a
	// manual tool, but Uninstall still stops it if it happens to be
	// running.
	IdleAssetName = "StayWakeBlackScreenIdle.exe"
	MainAssetName = "StayWakeBlackScreen.exe"

	runValueName = "StayWakeBlackScreenIdle"
)

// DefaultInstallDir returns %LOCALAPPDATA%\StayWakeBlackScreen.
func DefaultInstallDir() (string, error) {
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
	InstallDir  string // defaults to DefaultInstallDir() if empty
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
	installDir := opts.InstallDir
	if installDir == "" {
		var err error
		installDir, err = DefaultInstallDir()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("creating install dir: %w", err)
	}
	targetPath := filepath.Join(installDir, IdleAssetName)

	fmt.Printf("Looking up latest release of %s/%s...\n", repoOwner, repoName)
	rel, err := latestRelease(opts.GitHubToken)
	if err != nil {
		return fmt.Errorf("fetching latest release: %w", err)
	}
	var downloadURL string
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, IdleAssetName) {
			downloadURL = a.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("release %s has no asset named %s", rel.TagName, IdleAssetName)
	}
	fmt.Printf("Downloading %s (%s)...\n", rel.TagName, downloadURL)

	tmpPath := targetPath + ".download"
	if err := downloadFile(downloadURL, opts.GitHubToken, tmpPath); err != nil {
		return fmt.Errorf("downloading asset: %w", err)
	}

	fmt.Println("Stopping any already-running instance...")
	if err := terminateRunning(IdleAssetName); err != nil {
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
		cmd := exec.Command(targetPath)
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting %s: %w", targetPath, err)
		}
	}

	fmt.Println("Done.")
	return nil
}

// UninstallOptions configures Uninstall.
type UninstallOptions struct {
	InstallDir string // defaults to DefaultInstallDir() if empty
	KeepFiles  bool   // remove autostart and stop the process, but leave the installed files in place
}

// Uninstall reverses Install: removes the autostart registry entry,
// terminates any running copy of StayWakeBlackScreenIdle.exe or
// StayWakeBlackScreen.exe, and (unless KeepFiles) deletes the installed
// files.
func Uninstall(opts UninstallOptions) error {
	installDir := opts.InstallDir
	if installDir == "" {
		var err error
		installDir, err = DefaultInstallDir()
		if err != nil {
			return err
		}
	}

	fmt.Println("Removing autostart entry...")
	if err := removeAutostart(); err != nil {
		return fmt.Errorf("removing autostart: %w", err)
	}

	fmt.Println("Stopping any running instance...")
	if err := terminateRunning(IdleAssetName); err != nil {
		return fmt.Errorf("stopping %s: %w", IdleAssetName, err)
	}
	if err := terminateRunning(MainAssetName); err != nil {
		return fmt.Errorf("stopping %s: %w", MainAssetName, err)
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

func latestRelease(token string) (*ghRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "stay-wake-setup")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GitHub API returned %s: %s", resp.Status, string(body))
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func downloadFile(url, token, destPath string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "stay-wake-setup")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return err
	}
	return nil
}

// replaceFile moves tmpPath onto targetPath, retrying briefly: the target
// may still be momentarily locked right after terminateRunning killed the
// process that had it open/mapped.
func replaceFile(tmpPath, targetPath string) error {
	var lastErr error
	for i := 0; i < 10; i++ {
		if err := os.Rename(tmpPath, targetPath); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	os.Remove(tmpPath)
	return lastErr
}

func setAutostart(targetPath string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(runValueName, `"`+targetPath+`"`)
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
