import { useCallback, useState } from 'react';
import { cn } from '@/lib/cn';

/**
 * Generic Kanban board. Native HTML5 drag-and-drop — no library dep.
 *
 * Caller supplies columns (already grouped), a card renderer, and an
 * onMoveCard async callback. Optimistic UI lives one level up — the
 * Kanban itself just emits the move event; the parent handles
 * re-fetching / mutating local state.
 */
export interface KanbanColumn<T> {
  id: string;
  label: string;
  /** Optional small tone hint to color the column header. */
  tone?: 'neutral' | 'info' | 'warning' | 'success' | 'danger' | 'accent';
  /** Optional totals string shown next to the count (e.g. money sum). */
  totalLabel?: string;
  items: T[];
}

export interface KanbanProps<T> {
  columns: KanbanColumn<T>[];
  renderCard: (item: T) => React.ReactNode;
  getCardId: (item: T) => string;
  onMoveCard?: (cardId: string, fromColumnId: string, toColumnId: string) => Promise<void> | void;
  /** Allow drop into this column? Default: always true except same-column.
   *  Use this to lock terminal columns (e.g. block manual drag into "closed_lost"). */
  canDropInto?: (cardId: string, fromColumnId: string, toColumnId: string) => boolean;
}

export function Kanban<T>({
  columns, renderCard, getCardId, onMoveCard, canDropInto,
}: KanbanProps<T>) {
  // Track which card is being dragged + which column is the current hover
  // target, so we can paint subtle visual feedback. (HTML5 DnD doesn't
  // hand us either of these for free.)
  const [draggingId, setDraggingId] = useState<string | null>(null);
  const [hoverColumn, setHoverColumn] = useState<string | null>(null);

  const handleDragStart = useCallback((e: React.DragEvent, cardId: string, fromColumnId: string) => {
    e.dataTransfer.setData('application/x-kanban-card', JSON.stringify({ cardId, fromColumnId }));
    e.dataTransfer.effectAllowed = 'move';
    setDraggingId(cardId);
  }, []);

  const handleDragEnd = useCallback(() => {
    setDraggingId(null);
    setHoverColumn(null);
  }, []);

  const handleDragOverColumn = useCallback((e: React.DragEvent, columnId: string) => {
    // preventDefault enables drop. The reason HTML5 DnD requires this
    // explicit opt-in is to block accidental drops on text inputs etc.
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    setHoverColumn(columnId);
  }, []);

  const handleDropOnColumn = useCallback(async (e: React.DragEvent, toColumnId: string) => {
    e.preventDefault();
    const raw = e.dataTransfer.getData('application/x-kanban-card');
    setDraggingId(null);
    setHoverColumn(null);
    if (!raw) return;
    let payload: { cardId: string; fromColumnId: string };
    try { payload = JSON.parse(raw); } catch { return; }
    if (payload.fromColumnId === toColumnId) return;
    if (canDropInto && !canDropInto(payload.cardId, payload.fromColumnId, toColumnId)) return;
    if (onMoveCard) await onMoveCard(payload.cardId, payload.fromColumnId, toColumnId);
  }, [onMoveCard, canDropInto]);

  return (
    <div className="flex gap-3 overflow-x-auto pb-3" role="list">
      {columns.map((col) => {
        const isHover = hoverColumn === col.id;
        return (
          <div
            key={col.id}
            role="listitem"
            className={cn(
              'flex-shrink-0 w-[280px] rounded-lg border bg-surface-soft flex flex-col',
              isHover ? 'border-accent ring-2 ring-accent/30' : 'border-hairline',
            )}
            onDragOver={(e) => handleDragOverColumn(e, col.id)}
            onDragLeave={() => hoverColumn === col.id && setHoverColumn(null)}
            onDrop={(e) => handleDropOnColumn(e, col.id)}
          >
            <header className={cn(
              'px-3 py-2 rounded-t-lg border-b border-hairline flex items-baseline justify-between gap-2',
              toneClass(col.tone),
            )}>
              <div className="text-body-sm font-medium text-ink truncate">{col.label}</div>
              <div className="flex items-baseline gap-2 text-caption text-stone font-mono">
                <span className="num">{col.items.length}</span>
                {col.totalLabel && <span className="text-text-tertiary">·</span>}
                {col.totalLabel && <span className="num">{col.totalLabel}</span>}
              </div>
            </header>
            <div className="flex-1 px-2 py-2 space-y-2 min-h-[120px] max-h-[70vh] overflow-y-auto">
              {col.items.length === 0 && (
                <div className="text-caption text-stone text-center py-6 italic">Drop cards here</div>
              )}
              {col.items.map((item) => {
                const id = getCardId(item);
                const isDragging = draggingId === id;
                return (
                  <div
                    key={id}
                    draggable
                    onDragStart={(e) => handleDragStart(e, id, col.id)}
                    onDragEnd={handleDragEnd}
                    className={cn(
                      'rounded-md bg-canvas border border-hairline shadow-sm hover:shadow-md cursor-grab',
                      'transition-shadow',
                      isDragging && 'opacity-50 cursor-grabbing',
                    )}
                  >
                    {renderCard(item)}
                  </div>
                );
              })}
            </div>
          </div>
        );
      })}
    </div>
  );
}

// Tone → header background. Subtle so the kanban reads as one board, not
// a rainbow.
function toneClass(tone: KanbanColumn<unknown>['tone']): string {
  switch (tone) {
    case 'info':    return 'bg-info/5';
    case 'warning': return 'bg-warning/5';
    case 'success': return 'bg-success/5';
    case 'danger':  return 'bg-danger/5';
    case 'accent':  return 'bg-accent/5';
    default:        return 'bg-surface';
  }
}
