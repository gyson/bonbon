package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"bonbon/internal/history"
	agent "bonbon/internal/runtime"
)

func expandWorkspace(workspace string) (string, error) {
	if workspace == "~" || strings.HasPrefix(workspace, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve workspace home directory: %w", err)
		}
		workspace = filepath.Join(home, strings.TrimPrefix(workspace[1:], "/"))
	}
	if !filepath.IsAbs(workspace) {
		return "", errors.New("use an absolute workspace path or ~/path from the server's home directory")
	}
	return workspace, nil
}

func (s *Server) ensureGeneral() error {
	workspace := filepath.Join(s.info.DataDir, "workspaces", "general")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		return err
	}
	canonical, err := agent.Canonical(workspace)
	if err != nil {
		return err
	}
	return s.store.EnsureGeneral(canonical)
}

func (s *Server) addProject(name, workspace string) (history.Project, error) {
	path, err := expandWorkspace(workspace)
	if err != nil {
		return history.Project{}, err
	}
	path, err = agent.Canonical(path)
	if err != nil {
		return history.Project{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(path)
	}
	projects, err := s.store.Projects()
	if err != nil {
		return history.Project{}, err
	}
	for _, p := range projects {
		if p.Workspace == path {
			return history.Project{}, fmt.Errorf("this folder already belongs to project %q", p.Name)
		}
	}
	return s.store.CreateProject(name, path)
}
