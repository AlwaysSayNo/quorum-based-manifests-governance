package main

import (
	"bufio"
	"os"
	"strings"
)

// parseCommands reads the input file and separates it into INIT and MAIN lists.
// Any command may span multiple lines (e.g. Deployment manifests) -- uses heredoc support.
func parseCommands(path string) (init []string, main []string, err error) {
	// Init scanner with buffer 1 MB for multi-line commands
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	var (
		current    *[]string
		cmdBuf     strings.Builder
		inHeredoc  bool
		heredocEnd string
	)

	// flush the current command buffer into the current list (if non-empty)
	flush := func() {
		s := strings.TrimSpace(cmdBuf.String())
		if s != "" && current != nil {
			*current = append(*current, s)
		}
		cmdBuf.Reset()
	}

	for scanner.Scan() {
		line := scanner.Text()

		// Section headers
		// Read init header
		if strings.TrimSpace(line) == initHeader {
			flush()
			current = &init
			continue
		}
		// Read main header
		if strings.TrimSpace(line) == mainHeader {
			flush()
			current = &main
			continue
		}

		// Skip lines before the first header
		if current == nil {
			continue
		}

		// Inside a heredoc block. Accumulates lines until sees the end tag
		if inHeredoc {
			cmdBuf.WriteString(line)
			cmdBuf.WriteString("\n")
			if strings.TrimSpace(line) == heredocEnd {
				inHeredoc = false
				flush()
			}
			continue
		}

		// Detect heredoc start (<<'EOF' or <<EOF)
		if idx := strings.Index(line, "<<"); idx >= 0 {
			tag := strings.TrimSpace(line[idx+2:])
			tag = strings.Trim(tag, "'\"")
			if tag != "" {
				// Flush any previous single-line command
				flush()

				// Start accumulating the heredoc content
				inHeredoc = true
				heredocEnd = tag
				cmdBuf.WriteString(line)
				cmdBuf.WriteString("\n")
				continue
			}
		}

		// Regular single-line command
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Flush any previous command
		flush()
		cmdBuf.WriteString(trimmed)
	}
	flush()

	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return init, main, nil
}
