package util

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"

	"github.com/grafana/e2e"
)

func CopyTemplateToSharedDir(s *e2e.Scenario, src, dst string, data any) (string, error) {
	tmpl, err := template.ParseFiles(src)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", err
	}

	return writeFileToSharedDir(s, dst, buf.Bytes())
}

func writeFileToSharedDir(s *e2e.Scenario, dst string, content []byte) (string, error) {
	dst = filepath.Join(s.SharedDir(), dst)

	// NOTE: since the integration tests are setup outside of the container
	// before container execution, the permissions within the container must be
	// able to read the configuration.

	// Ensure the entire path of directories exists
	err := os.MkdirAll(filepath.Dir(dst), os.ModePerm)
	if err != nil {
		return "", err
	}

	err = os.WriteFile(dst, content, os.ModePerm)
	if err != nil {
		return "", err
	}

	return dst, nil
}
