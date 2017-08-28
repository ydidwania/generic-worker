package main

import (
	"path/filepath"
)

func copyArtifactTo(src, dest string) [][]string {
	sourcePath := filepath.Join(testdataDir, src)
	return [][]string{
		{
			"mkdir",
			"-p",
			filepath.Dir(dest),
		},
		{
			"cp",
			sourcePath,
			dest,
		},
	}
}
