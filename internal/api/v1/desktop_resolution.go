package v1

import "fmt"

const DefaultDesktopResolution = "1024x640"

func ValidateDesktopResolution(value string) error {
	switch value {
	case "", DefaultDesktopResolution, "1280x720":
		return nil
	default:
		return fmt.Errorf("desktopResolution must be 1024x640 or 1280x720")
	}
}
