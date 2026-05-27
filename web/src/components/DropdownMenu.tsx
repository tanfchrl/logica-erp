import * as RDM from '@radix-ui/react-dropdown-menu';
import { Check, ChevronRight } from 'lucide-react';
import { cn } from '@/lib/cn';

export const DropdownMenu = RDM.Root;
export const DropdownMenuTrigger = RDM.Trigger;
export const DropdownMenuGroup = RDM.Group;
export const DropdownMenuPortal = RDM.Portal;
export const DropdownMenuSub = RDM.Sub;
export const DropdownMenuRadioGroup = RDM.RadioGroup;

export function DropdownMenuContent({ className, sideOffset = 6, ...props }: React.ComponentPropsWithoutRef<typeof RDM.Content>) {
  return (
    <RDM.Portal>
      <RDM.Content
        sideOffset={sideOffset}
        className={cn(
          'z-50 min-w-[10rem] surface-card p-1 animate-scale-in',
          className,
        )}
        {...props}
      />
    </RDM.Portal>
  );
}

export function DropdownMenuItem({ className, inset, ...props }: React.ComponentPropsWithoutRef<typeof RDM.Item> & { inset?: boolean }) {
  return (
    <RDM.Item
      className={cn(
        'flex items-center gap-2 px-2.5 py-1.5 rounded text-body text-text-primary cursor-default select-none',
        'data-[highlighted]:bg-accent-soft data-[highlighted]:text-accent',
        'data-[disabled]:opacity-50 data-[disabled]:pointer-events-none',
        'focus:outline-none',
        inset && 'pl-8',
        className,
      )}
      {...props}
    />
  );
}

export function DropdownMenuSeparator(props: React.ComponentPropsWithoutRef<typeof RDM.Separator>) {
  return <RDM.Separator className="h-px my-1 bg-border" {...props} />;
}

export function DropdownMenuLabel({ className, ...props }: React.ComponentPropsWithoutRef<typeof RDM.Label>) {
  return <RDM.Label className={cn('px-2.5 py-1.5 text-caption font-medium text-text-tertiary', className)} {...props} />;
}

export function DropdownMenuCheckboxItem({ className, children, checked, ...props }: React.ComponentPropsWithoutRef<typeof RDM.CheckboxItem>) {
  return (
    <RDM.CheckboxItem
      checked={checked}
      className={cn(
        'flex items-center gap-2 pl-8 pr-2.5 py-1.5 rounded text-body cursor-default select-none relative',
        'data-[highlighted]:bg-accent-soft data-[highlighted]:text-accent',
        'focus:outline-none',
        className,
      )}
      {...props}
    >
      <span className="absolute left-2 inline-flex items-center justify-center">
        <RDM.ItemIndicator><Check className="size-3.5" /></RDM.ItemIndicator>
      </span>
      {children}
    </RDM.CheckboxItem>
  );
}

export function DropdownMenuSubTrigger({ className, children, ...props }: React.ComponentPropsWithoutRef<typeof RDM.SubTrigger>) {
  return (
    <RDM.SubTrigger
      className={cn(
        'flex items-center gap-2 px-2.5 py-1.5 rounded text-body text-text-primary cursor-default select-none',
        'data-[highlighted]:bg-accent-soft data-[highlighted]:text-accent',
        className,
      )}
      {...props}
    >
      {children}
      <ChevronRight className="ml-auto size-3.5" />
    </RDM.SubTrigger>
  );
}

export function DropdownMenuSubContent({ className, ...props }: React.ComponentPropsWithoutRef<typeof RDM.SubContent>) {
  return (
    <RDM.Portal>
      <RDM.SubContent
        className={cn('z-50 min-w-[10rem] surface-card p-1 animate-scale-in', className)}
        {...props}
      />
    </RDM.Portal>
  );
}
