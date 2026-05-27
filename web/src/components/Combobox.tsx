import { useState, useMemo } from 'react';
import * as RPopover from '@radix-ui/react-popover';
import { Command } from 'cmdk';
import { Check, ChevronsUpDown, Search } from 'lucide-react';
import { cn } from '@/lib/cn';

export interface ComboboxOption {
  value: string;
  label: string;
  description?: string;
}

interface ComboboxProps {
  options: ComboboxOption[];
  value?: string | null;
  onChange: (value: string | null) => void;
  placeholder?: string;
  emptyText?: string;
  searchPlaceholder?: string;
  disabled?: boolean;
  className?: string;
  id?: string;
}

/**
 * Link-field combobox: searchable single-select.
 * Built on Radix Popover + cmdk for keyboard nav + fuzzy filter.
 */
export function Combobox({
  options, value, onChange, placeholder = 'Select…', emptyText = 'No options',
  searchPlaceholder = 'Search…', disabled, className, id,
}: ComboboxProps) {
  const [open, setOpen] = useState(false);
  const selected = useMemo(() => options.find((o) => o.value === value), [options, value]);

  return (
    <RPopover.Root open={open} onOpenChange={setOpen}>
      <RPopover.Trigger
        id={id}
        disabled={disabled}
        className={cn(
          'input-base flex items-center justify-between gap-2 cursor-default text-left',
          !selected && 'text-text-tertiary',
          className,
        )}
      >
        <span className="truncate">{selected?.label ?? placeholder}</span>
        <ChevronsUpDown className="size-3.5 text-text-tertiary shrink-0" />
      </RPopover.Trigger>

      <RPopover.Portal>
        <RPopover.Content
          align="start"
          sideOffset={4}
          className="z-50 surface-card !p-0 overflow-hidden w-[var(--radix-popover-trigger-width)] min-w-[240px] animate-scale-in"
        >
          <Command>
            <div className="flex items-center gap-2 px-3 border-b border-border">
              <Search className="size-3.5 text-text-tertiary shrink-0" />
              <Command.Input
                autoFocus
                placeholder={searchPlaceholder}
                className="!border-0 !py-2 !px-0 text-body"
              />
            </div>
            <Command.List className="max-h-[280px] overflow-y-auto p-1">
              <Command.Empty>{emptyText}</Command.Empty>
              {options.map((opt) => (
                <Command.Item
                  key={opt.value}
                  value={`${opt.label} ${opt.description ?? ''}`}
                  onSelect={() => { onChange(opt.value); setOpen(false); }}
                >
                  <div className="flex-1 min-w-0">
                    <div className="text-body text-text-primary truncate">{opt.label}</div>
                    {opt.description && (
                      <div className="text-caption text-text-tertiary truncate">{opt.description}</div>
                    )}
                  </div>
                  {opt.value === value && <Check className="size-4 text-accent shrink-0" />}
                </Command.Item>
              ))}
            </Command.List>
          </Command>
        </RPopover.Content>
      </RPopover.Portal>
    </RPopover.Root>
  );
}
