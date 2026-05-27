import { Construction, Sparkles } from 'lucide-react';
import { PageHeader } from '@/components/PageHeader';
import { Card } from '@/components/Card';
import { Button } from '@/components/Button';
import { Kbd } from '@/components/Kbd';
import { useUI } from '@/store/ui';

export function ModuleStub({ module }: { module: string }) {
  const togglePalette = useUI((s) => s.togglePalette);
  return (
    <>
      <PageHeader title={module} subtitle="Backend is live — UI lands in a future phase." />
      <div className="flex-1 px-6 lg:px-8 pt-6 pb-8">
        <Card className="!p-12 text-center">
          <div className="inline-flex size-12 items-center justify-center rounded-full bg-accent-soft text-accent">
            <Construction className="size-6" />
          </div>
          <h2 className="mt-4 text-section-head text-text-primary">{module} UI coming next</h2>
          <p className="mt-1 text-body text-text-secondary max-w-md mx-auto">
            All of {module}'s backend doctypes are implemented and accessible via the API. The styled UI will land per the post-Phase-0 build plan.
          </p>
          <div className="mt-6 inline-flex items-center gap-2">
            <Button variant="secondary" onClick={togglePalette}>
              <Sparkles className="size-4" /> Try the command palette
              <Kbd>⌘K</Kbd>
            </Button>
          </div>
        </Card>
      </div>
    </>
  );
}
