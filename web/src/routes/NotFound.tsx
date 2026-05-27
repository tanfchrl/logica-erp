import { Link } from '@tanstack/react-router';
import { Compass, Home } from 'lucide-react';
import { Button } from '@/components/Button';
import { Card, CardTitle, CardDescription } from '@/components/Card';
import { Kbd } from '@/components/Kbd';
import { useUI } from '@/store/ui';

export function NotFoundPage() {
  const togglePalette = useUI((s) => s.togglePalette);
  return (
    <div className="min-h-screen flex items-center justify-center px-4 surface-gradient">
      <Card className="w-full max-w-md text-center !p-8">
        <div className="inline-flex size-12 items-center justify-center rounded-full bg-accent-soft text-accent mx-auto">
          <Compass className="size-6" />
        </div>
        <CardTitle className="mt-4">Page not found</CardTitle>
        <CardDescription className="mt-1">
          The URL you tried doesn't match any registered route.
        </CardDescription>
        <div className="mt-6 flex flex-col sm:flex-row items-center justify-center gap-2">
          <Button asChild>
            <Link to={'/' as never}><Home className="size-4" /> Back to dashboard</Link>
          </Button>
          <Button variant="secondary" onClick={togglePalette}>
            Open command palette <Kbd>⌘K</Kbd>
          </Button>
        </div>
      </Card>
    </div>
  );
}
