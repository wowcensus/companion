package companion

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var installPath = UserInstallPath

// UserInstallPath is kept separate from the settings directory so uninstalling
// the program does not discard upload receipts or the device registration.
func UserInstallPath() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return `C:\WoWCensus\wowcensus-companion.exe`, nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Applications", "WoWCensus Companion.app"), nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		name := "wowcensus-companion"
		if os.Getenv("APPIMAGE") != "" {
			name += ".AppImage"
		}
		return filepath.Join(home, ".local", "opt", "wowcensus-companion", name), nil
	}
}

func legacyWindowsInstallPath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "Programs", "WoWCensus Companion", "wowcensus-companion.exe"), nil
}

func sameInstallPath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func copyInstallFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst+".installing", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst + ".installing")
		return fmt.Errorf("copying app: %v %v", copyErr, closeErr)
	}
	if err := os.Rename(dst+".installing", dst); err != nil {
		_ = os.Remove(dst + ".installing")
		return err
	}
	return nil
}

func installLinuxLauncher(target string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`).Replace(target) + `"`
	entry := "[Desktop Entry]\nType=Application\nName=WoWCensus Companion\nExec=" + quoted + "\nTerminal=false\nCategories=Game;Utility;\n"
	return writeFileAtomic(filepath.Join(home, ".local", "share", "applications", "wowcensus-companion.desktop"), []byte(entry), 0o644)
}

// InstallToUserHome installs a downloaded copy and returns the installed app
// to launch. An already installed app simply continues its normal startup.
func InstallToUserHome(args []string) (Relaunch, bool, error) {
	exe, err := executablePath()
	if err != nil {
		return Relaunch{}, false, err
	}
	target, err := installPath()
	if err != nil {
		return Relaunch{}, false, err
	}
	if runtime.GOOS == "darwin" {
		bundle := appBundle(exe)
		if bundle == "" {
			return Relaunch{}, false, fmt.Errorf("the Mac app must be run from its .app bundle")
		}
		if sameInstallPath(bundle, target) {
			return Relaunch{}, false, nil
		}
		if _, err := os.Stat(target); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return Relaunch{}, false, err
			}
			if out, err := exec.Command("ditto", bundle, target).CombinedOutput(); err != nil {
				return Relaunch{}, false, fmt.Errorf("copying Mac app: %v: %s", err, out)
			}
		} else if err != nil {
			return Relaunch{}, false, err
		}
		launchArgs := []string{"-n", target}
		if len(args) > 0 {
			launchArgs = append(launchArgs, "--args")
			launchArgs = append(launchArgs, args...)
		}
		return Relaunch{Path: "open", Args: launchArgs}, true, nil
	}
	source := exe
	if runtime.GOOS == "windows" {
		legacy, err := legacyWindowsInstallPath()
		if err != nil {
			return Relaunch{}, false, err
		}
		if sameInstallPath(source, legacy) {
			return Relaunch{}, false, nil
		}
	}
	if runtime.GOOS == "linux" && os.Getenv("APPIMAGE") != "" {
		source = os.Getenv("APPIMAGE")
	}
	if sameInstallPath(source, target) {
		return Relaunch{}, false, nil
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := copyInstallFile(source, target); err != nil {
			return Relaunch{}, false, err
		}
	} else if err != nil {
		return Relaunch{}, false, err
	}
	if runtime.GOOS == "linux" {
		if err := installLinuxLauncher(target); err != nil {
			return Relaunch{}, false, err
		}
	}
	return Relaunch{Path: target, Args: args}, true, nil
}
