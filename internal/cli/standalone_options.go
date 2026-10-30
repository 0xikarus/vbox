package cli

import (
	"fmt"
	v1 "github.com/0xikarus/vmbox-service/internal/api/v1"
	"github.com/0xikarus/vmbox-service/internal/instance"
	"strings"
)

func standaloneTools(value, proxy string) ([]string, error) {
	var tools []string
	for _, tool := range strings.Split(value, ",") {
		if tool = strings.TrimSpace(tool); tool != "" {
			tools = append(tools, tool)
		}
	}
	switch proxy {
	case "", "none":
	case "tor":
		tools = append(tools, "tor-socks5")
	case "warp":
		tools = append(tools, "cloudflare-warp")
	default:
		return nil, fmt.Errorf("proxy must be none, tor or warp")
	}
	return tools, v1.ValidateTools(tools)
}
func standalonePasswordArchive(password string) ([]byte, error) {
	return instance.PasswordArchive(password)
}
