package boxruntime

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed captcha_extension/*
var captchaExtensionFS embed.FS

const captchaExtensionName = "vmbox-captcha-guard"

// InstallCaptchaExtension writes the bundled challenge-detection extension to a
// stable per-box directory and returns its path for --load-extension. The
// extension only annotates pages with a detection marker and shows the raw
// detected values per captcha in its toolbar popup; it never reads solver
// inputs and never solves or submits a challenge.
func InstallCaptchaExtension(home string) (string, error) {
	dir := filepath.Join(home, ".config", "vmbox", "extensions", captchaExtensionName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	entries, err := captchaExtensionFS.ReadDir("captcha_extension")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := captchaExtensionFS.ReadFile("captcha_extension/" + entry.Name())
		if err != nil {
			return "", err
		}
		if err := writeTextAtomic(filepath.Join(dir, entry.Name()), string(data), 0600); err != nil {
			return "", err
		}
	}
	return dir, nil
}
