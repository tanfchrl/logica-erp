import { Outlet } from '@tanstack/react-router';
import { Sidebar } from './Sidebar';
import { TopChrome } from './TopChrome';
import { CommandPalette } from './CommandPalette';
import { NudgeBar } from '@/components/NudgeBar';

export function AppShell() {
  return (
    <div className="flex min-h-screen bg-surface-soft">
      <Sidebar />
      <main className="flex-1 min-w-0 flex flex-col">
        {/* Persistent top chrome (search / notifications / avatar) */}
        <TopChrome />
        {/* Ambient nudges from the agent layer. Hides itself when empty. */}
        <NudgeBar />
        <Outlet />
      </main>
      <CommandPalette />
    </div>
  );
}
