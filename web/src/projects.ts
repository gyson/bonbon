import type { Project, SessionInfo } from './protocol.js';

// Project matches expose its recent sessions; session matches keep their group.
export function sessionGroups(projects: Project[], sessions: SessionInfo[], filter: string) {
  const query = filter.trim().toLowerCase();
  const matches = (session: SessionInfo) => `${session.title} ${session.workspace} ${session.id}`.toLowerCase().includes(query);
  const groups: { project: Project | null; sessions: SessionInfo[] }[] = projects.map(project => {
    const projectMatches = `${project.name} ${project.workspace}`.toLowerCase().includes(query);
    return { project, sessions: sessions.filter(session => session.projectId === project.id && (projectMatches || matches(session))) };
  }).filter(group => !query || group.sessions.length || `${group.project.name} ${group.project.workspace}`.toLowerCase().includes(query));
  const known = new Set(projects.map(project => project.id));
  const standalone = sessions.filter(session => !known.has(session.projectId) && matches(session));
  if (standalone.length) groups.push({ project: null, sessions: standalone });
  return groups;
}
