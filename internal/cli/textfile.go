// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"bytes"
	"fmt"
	"os"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// readTextOrFile returns text when set, otherwise the contents of filePath with
// any UTF-8 byte order mark removed. Both empty yields an empty string.
func readTextOrFile(text, filePath string) (string, error) {
	if text != "" {
		return text, nil
	}
	if filePath == "" {
		return "", nil
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %q: %w", filePath, err)
	}
	return string(bytes.TrimPrefix(data, utf8BOM)), nil
}
