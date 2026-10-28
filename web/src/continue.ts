import type { Tool } from './protocol.js';

// A new terminal can start only after the latest terminal of a session ended.
export const endedStatuses: readonly string[] = ['exited', 'stopped', 'failed', 'interrupted'];

export interface ContinueTarget { status: string; archived?: boolean; run?: { id: string } }

export function canContinue(session: ContinueTarget | null, live: boolean): boolean {
  return !!session?.run && !session.archived && !live && endedStatuses.includes(session.status);
}

// Prefer the tool that the session started with, if it still exists.
export function defaultContinueTool(tools: readonly Tool[], preferred: string | undefined, fallback: string): string {
  if (preferred !== undefined && (preferred === '' || tools.some(tool => tool.id === preferred))) return preferred;
  return tools.some(tool => tool.id === fallback) ? fallback : '';
}

// Explain what Start submits to the new shell. BonBon never sends the prompt on its own
// without a continue command, because it cannot detect when an agent is ready.
export function continueHint(tool: Tool | undefined): string {
  if (!tool) return 'Shell starts an interactive shell and submits no command. Start an agent, then send the prompt when it is ready. Handoff prompt in the message editor shows it again.';
  if (tool.continueCommand) return `BonBon submits “${tool.continueCommand}” once, with the quoted prompt below in place of {prompt}.`;
  return `${tool.name || 'This tool'} has no continue command, so BonBon submits “${tool.command}” and does not send the prompt. When the agent is ready, send the prompt. Handoff prompt in the message editor shows it again. To send it automatically, add a continue command in Settings.`;
}
