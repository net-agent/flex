// Package domain contains the virtual-node name validation shared by the
// switcher registry, presence center and the v4 handshake.
package domain

import (
	"errors"
	"strings"
)

// ErrInvalidDomain is returned for an empty, too long, or malformed name.
var ErrInvalidDomain = errors.New("invalid domain")

// Normalize lowercases and validates a virtual node name.
func Normalize(name string) (string, error) {
	name = strings.TrimSpace(strings.ToLower(name))

	if name == "" || len(name) > 63 {
		return "", ErrInvalidDomain
	}
	if name == "local" || name == "localhost" {
		return "", ErrInvalidDomain
	}
	if name[0] == '-' || name[0] == '_' || name[len(name)-1] == '-' || name[len(name)-1] == '_' {
		return "", ErrInvalidDomain
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '-' && c != '_' {
			return "", ErrInvalidDomain
		}
	}
	return name, nil
}
