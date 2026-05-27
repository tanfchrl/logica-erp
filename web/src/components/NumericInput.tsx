import { forwardRef, useState, useEffect } from 'react';
import { cn } from '@/lib/cn';

interface NumericInputProps {
  value?: string | number | null;
  onChange?: (raw: string) => void;
  placeholder?: string;
  disabled?: boolean;
  className?: string;
  // Display formatting hints
  currencyPrefix?: string;        // e.g. "Rp"
  decimalPlaces?: number;         // default 2
  align?: 'left' | 'right';       // default 'right' (numeric)
  // Allow negative? default true; the caller validates on submit.
  allowNegative?: boolean;
  id?: string;
  'aria-label'?: string;
}

const idIDNumber = (decimals: number) =>
  new Intl.NumberFormat('id-ID', { minimumFractionDigits: decimals, maximumFractionDigits: decimals });

/**
 * Numeric input with Indonesian-style display while focused-out, raw editing while focused.
 *  - on blur: '1234567.5' → 'Rp 1.234.567,50'
 *  - on focus: shows the raw decimal string so the user can edit comfortably
 *  - emits the RAW string on change so the caller can store it as-is (`decimal.Decimal`-friendly).
 */
export const NumericInput = forwardRef<HTMLInputElement, NumericInputProps>(
  ({ value, onChange, placeholder = '0', disabled, className, currencyPrefix,
     decimalPlaces = 2, align = 'right', allowNegative = true, id, ...aria }, _ref) => {
    const [focused, setFocused] = useState(false);
    const [draft, setDraft] = useState<string>(value !== null && value !== undefined ? String(value) : '');

    useEffect(() => {
      if (!focused) setDraft(value !== null && value !== undefined ? String(value) : '');
    }, [value, focused]);

    const display = (() => {
      if (focused) return draft;
      if (draft === '' || draft === null || draft === undefined) return '';
      const n = Number(draft);
      if (Number.isNaN(n)) return draft;
      const formatted = idIDNumber(decimalPlaces).format(n);
      return currencyPrefix ? `${currencyPrefix} ${formatted}` : formatted;
    })();

    return (
      <input
        type="text"
        inputMode="decimal"
        id={id}
        disabled={disabled}
        placeholder={placeholder}
        value={display}
        onFocus={() => setFocused(true)}
        onBlur={() => { setFocused(false); }}
        onChange={(e) => {
          // Accept digits, single dot, optional leading minus.
          let v = e.target.value.replace(/[^0-9.\-]/g, '');
          if (!allowNegative) v = v.replace(/-/g, '');
          // Keep only the first dot.
          const firstDot = v.indexOf('.');
          if (firstDot >= 0) v = v.slice(0, firstDot + 1) + v.slice(firstDot + 1).replace(/\./g, '');
          // Keep '-' only at the very start.
          v = v.replace(/(?!^)-/g, '');
          setDraft(v);
          onChange?.(v);
        }}
        className={cn(
          'input-base num',
          align === 'right' && 'text-right',
          className,
        )}
        {...aria}
      />
    );
  },
);
NumericInput.displayName = 'NumericInput';
