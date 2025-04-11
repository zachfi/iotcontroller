package util

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"

	"github.com/grafana/e2e"
)

func NewTargetServer(target, configFile, kubeConfig string) *e2e.HTTPService {
	var (
		httpPort = 3300
		grpcPort = 9090
	)

	args := []string{
		"-server.http-listen-port=" + fmt.Sprint(httpPort),
		"-server.grpc-listen-port=" + fmt.Sprint(grpcPort),
		"-config.file=" + filepath.Join(e2e.ContainerSharedDir, configFile),
		"-kubeconfig=" + filepath.Join(e2e.ContainerSharedDir, kubeConfig),
		"-log.level=debug",
		"-target=" + target,
	}

	s := e2e.NewHTTPService(
		target,
		image,
		e2e.NewCommandWithoutEntrypoint("/manager", args...),
		e2e.NewHTTPReadinessProbe(httpPort, "/ready", 200, 299),
		httpPort,
		grpcPort,
	)
	return s
}

func CopyFileToSharedDir(s *e2e.Scenario, src, dst string) error {
	content, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("unable to read local file %s: %w", src, err)
	}

	_, err = WriteFileToSharedDir(s, dst, content)
	return err
}

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

	return WriteFileToSharedDir(s, dst, buf.Bytes())
}

func WriteFileToSharedDir(s *e2e.Scenario, dst string, content []byte) (string, error) {
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
