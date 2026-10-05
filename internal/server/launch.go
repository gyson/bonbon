package server

import (
	"context"
	"errors"
	"path/filepath"

	"bonbon/internal/history"
	"bonbon/internal/protocol"
	agent "bonbon/internal/runtime"
)

func (s *Server) prepareProject(ctx context.Context, projectID string) (history.Session, error) {
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	project, err := s.store.Project(projectID)
	if err != nil {
		return history.Session{}, err
	}
	if existing, err := s.store.ProjectDraft(projectID); err == nil {
		return existing, nil
	} else if !errors.Is(err, history.ErrNotFound) {
		return history.Session{}, err
	}
	settings, err := s.store.Settings()
	if err != nil {
		return history.Session{}, err
	}
	p := history.Preparation{ToolID: settings.DefaultTool, ToolName: "Shell", Base: settings.Base}
	for _, tool := range settings.Tools {
		if tool.ID == p.ToolID {
			p.ToolName, p.Command = tool.Name, tool.Command
		}
	}
	workspace, err := agent.Canonical(project.Workspace)
	if err != nil {
		return history.Session{}, err
	}
	p.Worktree = settings.Worktree && inspectRepository(ctx, workspace).Available
	return s.store.CreatePreparation("", workspace, project.ID, p)
}

func (s *Server) savePreparation(request *protocol.Request) (history.Session, error) {
	s.launchMu.Lock()
	defer s.launchMu.Unlock()
	if request.Preparation == nil {
		return history.Session{}, errors.New("missing launch settings")
	}
	session, err := s.store.Session(request.Session)
	if err != nil {
		return history.Session{}, err
	}
	if session.Preparation == nil || session.Preparation.State != "draft" {
		return history.Session{}, errors.New("session already started; reload the session")
	}
	p, edit := *session.Preparation, request.Preparation
	if edit.ToolID != p.ToolID {
		p.ToolID, p.ToolName, p.Command = edit.ToolID, "Shell", ""
		if edit.ToolID != "" {
			settings, err := s.store.Settings()
			if err != nil {
				return history.Session{}, err
			}
			found := false
			for _, tool := range settings.Tools {
				if tool.ID == edit.ToolID {
					p.ToolName, p.Command, found = tool.Name, tool.Command, true
					break
				}
			}
			if !found {
				return history.Session{}, errors.New("tool no longer exists; choose Shell or a tool from Settings")
			}
		}
	}
	p.Revision, p.Worktree, p.Base, p.Branch = edit.Revision, edit.Worktree, edit.Base, edit.Branch
	return s.store.SavePreparation(session.ID, request.Name, p)
}

// Claim launch before filesystem or process effects. A lost connection can only
// reattach; neither reconnect nor restart repeats a possibly delivered command.
func (s *Server) launchPrepared(ctx context.Context, id string, revision int64, size protocol.Size) (history.Session, *agent.Launch, error) {
	session, err := s.store.Session(id)
	if err != nil {
		return session, nil, err
	}
	p := session.Preparation
	if p == nil || p.State != "draft" || p.Revision != revision {
		return session, nil, errors.New("session already started or launch settings changed; reload the session")
	}
	run, err := prepareRun(&protocol.Run{Workspace: session.Workspace, Title: session.Title, Command: p.Command, Size: size})
	if err != nil {
		return session, nil, err
	}
	source, err := agent.Canonical(run.Workspace)
	if err != nil {
		return session, nil, err
	}
	run.Workspace = source
	if err = s.store.SetPreparationState(id, revision, "draft", "creating"); err != nil {
		return session, nil, err
	}
	if p.Worktree {
		var worktree history.Worktree
		run.Workspace, worktree, err = s.createWorktree(ctx, id, source, p.Base, p.Branch)
		if err != nil {
			// Only return to preparation when no checkout could have been created.
			current, readErr := s.store.Session(id)
			if readErr == nil && (current.Worktree == nil || current.Worktree.State == "removed") {
				err = errors.Join(err, s.store.SetPreparationState(id, revision, "creating", "draft"))
			} else {
				err = errors.Join(err, s.store.SetPreparationState(id, revision, "creating", "interrupted"))
			}
			return session, nil, err
		}
		session.Worktree = &worktree
	}
	if run.Title == "" {
		run.Title = p.ToolName + " · " + filepath.Base(source)
	}
	if err = s.store.SetLaunchWorkspace(id, run.Title, run.Workspace); err != nil {
		return session, nil, err
	}
	if err = s.store.SetPreparationState(id, revision, "creating", "launched"); err != nil {
		return session, nil, err
	}
	session.Workspace, session.Title = run.Workspace, run.Title
	session.Preparation.State = "launched"
	return session, run, nil
}

func (s *Server) startPrepared(ctx context.Context, conn *protocol.Conn, request *protocol.Request) {
	s.launchMu.Lock()
	session, run, err := s.launchPrepared(ctx, request.Session, request.Revision, request.Size)
	if err != nil {
		s.launchMu.Unlock()
		s.reply(conn, nil, err)
		return
	}
	attach := s.sessionAttachment(ctx, conn, session, run)
	s.launchMu.Unlock()
	attach()
}
