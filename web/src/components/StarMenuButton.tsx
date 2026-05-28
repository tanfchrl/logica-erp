import { Star } from 'lucide-react';
import { Button } from '@/components/Button';
import { useStarredMenu } from '@/lib/starredMenu';

/**
 * Toolbar button for the doctype list page: stars the current list in the
 * user's sidebar "Starred" section. Filled-yellow when active.
 */
export function StarMenuButton({ path, label }: { path: string; label: string }) {
  const { isStarred, toggle, pending } = useStarredMenu();
  const starred = isStarred(path);
  return (
    <Button
      variant="secondary"
      onClick={() => toggle(path, label)}
      disabled={pending}
      title={starred ? 'Remove from sidebar Starred' : 'Add to sidebar Starred'}
      aria-pressed={starred}
    >
      <Star className={`size-4 ${starred ? 'fill-yellow-400 text-yellow-500' : ''}`} />
      {starred ? 'Starred' : 'Star'}
    </Button>
  );
}
