import { useTranslation } from 'react-i18next';
import { RotateCcw } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Field } from '@/components/Input';
import { useUI } from '@/store/ui';

/* Curated subset of common Indonesian timezones — sufficient for the SME
   market. Globe-wide tz picker can come later if anyone asks. */
const TIMEZONES = [
  { value: 'Asia/Jakarta',   label: 'Jakarta (WIB, UTC+7)' },
  { value: 'Asia/Makassar',  label: 'Makassar (WITA, UTC+8)' },
  { value: 'Asia/Jayapura',  label: 'Jayapura (WIT, UTC+9)' },
  { value: 'Asia/Singapore', label: 'Singapore (UTC+8)' },
  { value: 'UTC',            label: 'UTC' },
];

const LANGUAGES: { value: 'id-ID' | 'en-US'; label: string }[] = [
  { value: 'id-ID', label: 'Bahasa Indonesia' },
  { value: 'en-US', label: 'English (US)' },
];

const DATE_FORMATS: { value: 'dd/MM/yyyy' | 'yyyy-MM-dd' | 'd MMM yyyy'; label: string }[] = [
  { value: 'dd/MM/yyyy', label: '31/12/2026  (DMY, slash)' },
  { value: 'yyyy-MM-dd', label: '2026-12-31  (ISO 8601)' },
  { value: 'd MMM yyyy', label: '31 Dec 2026  (long)' },
];

const NUMBER_FORMATS: { value: 'id-ID' | 'en-US'; label: string }[] = [
  { value: 'id-ID', label: '1.234.567,89  (Indonesian)' },
  { value: 'en-US', label: '1,234,567.89  (US / international)' },
];

const WEEK_STARTS: { value: 0 | 1; label: string }[] = [
  { value: 1, label: 'Monday' },
  { value: 0, label: 'Sunday' },
];

export function LocalizationSection() {
  const { i18n } = useTranslation();
  const { locale, setLocale, resetLocale } = useUI();

  function changeLanguage(lang: 'id-ID' | 'en-US') {
    setLocale({ language: lang });
    void i18n.changeLanguage(lang);
  }

  const previewMoney = new Intl.NumberFormat(locale.numberFormat, {
    minimumFractionDigits: 2, maximumFractionDigits: 2,
  }).format(1234567.89);

  const previewDate = formatDateSample(new Date('2026-12-31T00:00:00Z'), locale.dateFormat, locale.timezone);
  const previewTime = new Intl.DateTimeFormat(locale.language, {
    timeStyle: 'short', timeZone: locale.timezone,
  }).format(new Date());

  return (
    <div className="space-y-6">
      <div className="flex items-end justify-end -mt-2">
        <Button variant="ghost" size="sm" onClick={() => { if (confirm('Reset localization to defaults?')) resetLocale(); }}>
          <RotateCcw className="size-3.5" /> Reset
        </Button>
      </div>

      <section>
        <div className="mb-3">
          <CardTitle>Language &amp; region</CardTitle>
          <CardDescription>Used for UI strings and default formatting.</CardDescription>
        </div>
        <Card>
          <div className="grid sm:grid-cols-2 gap-4">
            <Field label="Display language">
              <Select
                value={locale.language}
                options={LANGUAGES}
                onChange={(v) => changeLanguage(v as 'id-ID' | 'en-US')}
              />
            </Field>
            <Field label="Time zone">
              <Select
                value={locale.timezone}
                options={TIMEZONES}
                onChange={(v) => setLocale({ timezone: v })}
              />
            </Field>
          </div>
        </Card>
      </section>

      <section>
        <div className="mb-3">
          <CardTitle>Formatting</CardTitle>
          <CardDescription>How dates, numbers, and money are displayed across the app.</CardDescription>
        </div>
        <Card>
          <div className="grid sm:grid-cols-2 gap-4">
            <Field label="Date format">
              <Select
                value={locale.dateFormat}
                options={DATE_FORMATS}
                onChange={(v) => setLocale({ dateFormat: v as 'dd/MM/yyyy' | 'yyyy-MM-dd' | 'd MMM yyyy' })}
              />
            </Field>
            <Field label="Number format">
              <Select
                value={locale.numberFormat}
                options={NUMBER_FORMATS}
                onChange={(v) => setLocale({ numberFormat: v as 'id-ID' | 'en-US' })}
              />
            </Field>
            <Field label="First day of week">
              <Select
                value={String(locale.firstDayOfWeek)}
                options={WEEK_STARTS.map((w) => ({ value: String(w.value), label: w.label }))}
                onChange={(v) => setLocale({ firstDayOfWeek: Number(v) as 0 | 1 })}
              />
            </Field>
          </div>

          <div className="mt-5 pt-4 border-t border-hairline grid sm:grid-cols-3 gap-4">
            <PreviewCell label="Date" value={previewDate} />
            <PreviewCell label="Time"  value={previewTime} />
            <PreviewCell label="Amount" value={`Rp ${previewMoney}`} />
          </div>
        </Card>
      </section>
    </div>
  );
}

/* ----------------------------- helpers --------------------------------- */

function Select({
  value, options, onChange,
}: {
  value: string;
  options: { value: string; label: string }[];
  onChange: (v: string) => void;
}) {
  return (
    <select
      className="input-base appearance-none pr-8 bg-no-repeat bg-[right_0.75rem_center] bg-[length:1.25rem] cursor-pointer"
      style={{ backgroundImage: "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%23888' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'><polyline points='6 9 12 15 18 9'/></svg>\")" }}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
    </select>
  );
}

function PreviewCell({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-micro-uppercase text-stone mb-1">{label}</div>
      <div className="text-body-md text-ink num">{value}</div>
    </div>
  );
}

function formatDateSample(d: Date, fmt: 'dd/MM/yyyy' | 'yyyy-MM-dd' | 'd MMM yyyy', tz: string): string {
  const parts = new Intl.DateTimeFormat('en-GB', {
    day: '2-digit', month: '2-digit', year: 'numeric', timeZone: tz,
  }).formatToParts(d);
  const dd = parts.find((p) => p.type === 'day')!.value;
  const mm = parts.find((p) => p.type === 'month')!.value;
  const yy = parts.find((p) => p.type === 'year')!.value;
  if (fmt === 'dd/MM/yyyy') return `${dd}/${mm}/${yy}`;
  if (fmt === 'yyyy-MM-dd') return `${yy}-${mm}-${dd}`;
  // 'd MMM yyyy'
  const month = new Intl.DateTimeFormat('en-US', { month: 'short', timeZone: tz }).format(d);
  return `${parseInt(dd, 10)} ${month} ${yy}`;
}
